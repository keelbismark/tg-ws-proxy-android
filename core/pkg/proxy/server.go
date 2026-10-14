package proxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"tg-ws-proxy-core/pkg/config"
	"tg-ws-proxy-core/pkg/crypto"
	"tg-ws-proxy-core/pkg/faketls"
	"tg-ws-proxy-core/pkg/proto"
	"tg-ws-proxy-core/pkg/relay"
	"tg-ws-proxy-core/pkg/ws"
)

// Server coordinates listening for client connections, authentication, and routing to Telegram.
type Server struct {
	cfg      *config.Config
	listener net.Listener
	mu       sync.Mutex
	closed   bool
}

// NewServer initializes a new proxy server with the provided configuration.
func NewServer(cfg *config.Config) *Server {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return &Server{
		cfg: cfg,
	}
}

// Start begins listening on the configured address.
func (s *Server) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s failed: %w", s.cfg.ListenAddr, err)
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	// Close listener on context cancellation
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.closed = true
		if s.listener != nil {
			_ = s.listener.Close()
		}
		s.mu.Unlock()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return nil
			}
			log.Printf("[Proxy] Accept error: %v", err)
			continue
		}

		go s.handleClient(ctx, conn)
	}
}

// Close terminates the proxy server listener.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Server) handleClient(ctx context.Context, rawConn net.Conn) {
	relay.GlobalStats.IncConnectionsTotal()
	relay.GlobalStats.IncConnectionsActive()
	defer relay.GlobalStats.DecConnectionsActive()
	defer rawConn.Close()

	if tc, ok := rawConn.(*net.TCPConn); ok {
		_ = tc.SetNoDelay(true)
		_ = tc.SetReadBuffer(s.cfg.BufferSize)
		_ = tc.SetWriteBuffer(s.cfg.BufferSize)
	}

	// Read initial byte
	var firstByte [1]byte
	_ = rawConn.SetDeadline(time.Now().Add(s.cfg.Timeout))
	if _, err := io.ReadFull(rawConn, firstByte[:]); err != nil {
		return
	}

	var clientConn net.Conn = rawConn
	handshake := make([]byte, proto.HandshakeLen)

	if firstByte[0] == faketls.TLSRecordHandshake && s.cfg.IsFakeTLS {
		// Fake TLS ClientHello flow
		var hdrRest [4]byte
		if _, err := io.ReadFull(rawConn, hdrRest[:]); err != nil {
			return
		}
		recordLen := binary.BigEndian.Uint16(hdrRest[2:4])
		recordBody := make([]byte, recordLen)
		if _, err := io.ReadFull(rawConn, recordBody); err != nil {
			return
		}

		clientHello := make([]byte, 5+recordLen)
		clientHello[0] = firstByte[0]
		copy(clientHello[1:5], hdrRest[:])
		copy(clientHello[5:], recordBody)

		info, err := faketls.VerifyClientHello(clientHello, s.cfg.SecretBytes)
		if err != nil {
			logFakeTLSFailure()
			redirect := fmt.Sprintf("HTTP/1.1 301 Moved Permanently\r\nLocation: https://%s/\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", s.cfg.FakeTLSDomain)
			_, _ = rawConn.Write([]byte(redirect))
			return
		}

		serverHello, err := faketls.BuildServerHello(s.cfg.SecretBytes, info.ClientRandom, info.SessionID)
		if err != nil {
			log.Printf("[Proxy] BuildServerHello failed: %v", err)
			return
		}

		if _, err := rawConn.Write(serverHello); err != nil {
			return
		}

		fakeConn := faketls.NewFakeTLSConn(rawConn)
		clientConn = fakeConn

		_ = rawConn.SetDeadline(time.Now().Add(s.cfg.Timeout))
		if _, err := io.ReadFull(fakeConn, handshake); err != nil {
			log.Printf("[Proxy] Failed to read MTProto init inside TLS: %v", err)
			return
		}
	} else if s.cfg.IsFakeTLS {
		// Client connected with non-TLS byte while Fake TLS is enforced
		redirect := fmt.Sprintf("HTTP/1.1 301 Moved Permanently\r\nLocation: https://%s/\r\nContent-Length: 0\r\nConnection: close\r\n\r\n", s.cfg.FakeTLSDomain)
		_, _ = rawConn.Write([]byte(redirect))
		return
	} else {
		// Raw MTProto
		handshake[0] = firstByte[0]
		if _, err := io.ReadFull(rawConn, handshake[1:]); err != nil {
			return
		}
	}

	_ = rawConn.SetDeadline(time.Time{})
	_ = clientConn.SetDeadline(time.Time{})

	// Parse MTProto handshake
	res, err := crypto.TryHandshake(handshake, s.cfg.SecretBytes)
	if err != nil {
		relay.GlobalStats.IncConnectionsBad()
		log.Printf("[Proxy] Bad handshake: %v", err)
		return
	}

	dcID := res.DCID
	isMedia := res.IsMedia
	if s.cfg.ForceTestDC || dcID >= 10000 {
		if dcID >= 10000 {
			dcID -= 10000
		}
	}

	dcIdx := int16(dcID)
	if isMedia {
		dcIdx = -dcIdx
	}

	relayInit, err := crypto.GenerateRelayInit(res.ProtoTag, dcIdx)
	if err != nil {
		log.Printf("[Proxy] Failed to generate relay init: %v", err)
		return
	}

	cryptoCtx, err := crypto.NewCryptoContext(res.ClientDecPrekeyIV, s.cfg.SecretBytes, relayInit)
	if err != nil {
		log.Printf("[Proxy] Failed to derive crypto context: %v", err)
		return
	}

	splitter := proto.NewMsgSplitter(res.ProtoInt)

	kind := "chat"
	if isMedia {
		kind = "media"
	}

	// Fast concurrent upstream connection with staggered Happy-Eyeballs dialing
	upstreamWS, activeDomain, lastErr := s.dialFastUpstream(ctx, dcID, isMedia)
	if upstreamWS != nil {
		relay.GlobalStats.IncConnectionsWS()
		logConnected(dcID, isMedia, activeDomain)
		if err := upstreamWS.Send(relayInit); err != nil {
			_ = upstreamWS.Close()
			return
		}
		relay.BridgeWS(ctx, clientConn, upstreamWS, cryptoCtx, splitter, relay.GlobalStats, dcID, isMedia)
		return
	}

	// Fallback to Cloudflare Worker if direct and CF pool are unavailable
	if s.cfg.FallbackCF && len(s.cfg.CFWorkerDomains) > 0 {
		defaultIP := proto.DCDefaultIPs[dcID]
		workerTimeout := 4 * time.Second
		if s.cfg.Timeout < workerTimeout {
			workerTimeout = s.cfg.Timeout
		}
		for _, workerDomain := range s.cfg.CFWorkerDomains {
			workerPath := fmt.Sprintf("/apiws?dst=%s&dc=%d", defaultIP, dcID)
			wsConn, err := ws.Connect(ws.DialOptions{
				Host:       workerDomain,
				Domain:     workerDomain,
				Path:       workerPath,
				Secure:     true,
				Timeout:    workerTimeout,
				BufferSize: s.cfg.BufferSize,
			})
			if err == nil {
				relay.GlobalStats.IncConnectionsFallback()
				logConnected(dcID, isMedia, "CF Worker "+workerDomain)
				if err := wsConn.Send(relayInit); err != nil {
					_ = wsConn.Close()
					return
				}
				relay.BridgeWS(ctx, clientConn, wsConn, cryptoCtx, splitter, relay.GlobalStats, dcID, isMedia)
				return
			} else {
				lastErr = err
			}
		}
	}

	logUnreachable(dcID, kind, lastErr)
}

var (
	unreachableLogMu   sync.Mutex
	lastUnreachableLog time.Time
)

func logUnreachable(dcID int, kind string, err error) {
	unreachableLogMu.Lock()
	defer unreachableLogMu.Unlock()
	now := time.Now()
	if now.Sub(lastUnreachableLog) >= 5*time.Second {
		lastUnreachableLog = now
		log.Printf("[Proxy] DC%d (%s) unreachable: %v", dcID, kind, err)
	}
}

var (
	fakeTLSLogMu   sync.Mutex
	lastFakeTLSLog time.Time
)

func logFakeTLSFailure() {
	fakeTLSLogMu.Lock()
	defer fakeTLSLogMu.Unlock()
	now := time.Now()
	if now.Sub(lastFakeTLSLog) >= 5*time.Second {
		lastFakeTLSLog = now
		log.Printf("[Proxy] Fake TLS verify failed: secret mismatch -> redirecting")
	}
}

var (
	connLogMu    sync.Mutex
	lastChatLog  = make(map[int]time.Time)
	lastMediaLog = make(map[int]time.Time)
)

func logConnected(dcID int, isMedia bool, domain string) {
	connLogMu.Lock()
	defer connLogMu.Unlock()
	now := time.Now()
	if isMedia {
		if now.Sub(lastMediaLog[dcID]) >= 60*time.Second {
			lastMediaLog[dcID] = now
			log.Printf("[Proxy] Connected DC%d (media) via %s", dcID, domain)
		}
	} else {
		if now.Sub(lastChatLog[dcID]) >= 30*time.Second {
			lastChatLog[dcID] = now
			log.Printf("[Proxy] Connected DC%d (chat) via %s", dcID, domain)
		}
	}
}

type dialResult struct {
	ws     *ws.RawWebSocket
	domain string
	err    error
	index  int
}

func (s *Server) DialFastUpstreamForTest(ctx context.Context, dcID int, isMedia bool) (*ws.RawWebSocket, string, error) {
	return s.dialFastUpstream(ctx, dcID, isMedia)
}

// dialFastUpstream initiates concurrent staggered Happy-Eyeballs connection attempts
// to upstream gateways to prevent waiting on sequentially failing domains.
func (s *Server) dialFastUpstream(parentCtx context.Context, dcID int, isMedia bool) (*ws.RawWebSocket, string, error) {
	domains := globalBalancer.GetDomains(dcID, isMedia, s.cfg.FallbackCF)
	if len(domains) == 0 {
		return nil, "", errors.New("no upstream domains available")
	}

	// Limit to top 6 candidates to avoid socket churn
	if len(domains) > 6 {
		domains = domains[:6]
	}

	resultCh := make(chan dialResult, len(domains))
	cancels := make([]context.CancelFunc, len(domains))
	var cancelsMu sync.Mutex

	activeCount := 0
	candidateIdx := 0

	launchCandidate := func(idx int) {
		if idx >= len(domains) {
			return
		}
		domain := domains[idx]
		candCtx, cancel := context.WithCancel(parentCtx)

		cancelsMu.Lock()
		cancels[idx] = cancel
		cancelsMu.Unlock()

		activeCount++
		go func(d string, i int, cCtx context.Context) {
			domainTimeout := 4 * time.Second
			if IsDirectDomain(d) {
				domainTimeout = 1500 * time.Millisecond
			}
			if s.cfg.Timeout < domainTimeout {
				domainTimeout = s.cfg.Timeout
			}

			wsConn, err := ws.Connect(ws.DialOptions{
				Context:    cCtx,
				Host:       d,
				Domain:     d,
				Path:       proto.WSPath,
				Secure:     true,
				Timeout:    domainTimeout,
				BufferSize: s.cfg.BufferSize,
			})

			if cCtx.Err() != nil {
				if wsConn != nil {
					_ = wsConn.Close()
				}
				return
			}

			resultCh <- dialResult{ws: wsConn, domain: d, err: err, index: i}
		}(domain, idx, candCtx)
	}

	// Start first candidate immediately
	launchCandidate(0)
	candidateIdx = 1

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error

	for {
		select {
		case <-parentCtx.Done():
			cancelsMu.Lock()
			for _, c := range cancels {
				if c != nil {
					c()
				}
			}
			cancelsMu.Unlock()
			return nil, "", parentCtx.Err()

		case <-ticker.C:
			// Stagger next dial if candidates remain and we have room for concurrency
			if candidateIdx < len(domains) && activeCount < 3 {
				launchCandidate(candidateIdx)
				candidateIdx++
			}

		case res := <-resultCh:
			activeCount--
			if res.err == nil && res.ws != nil {
				// Cancel ONLY losing candidates, NOT the winner!
				cancelsMu.Lock()
				for i, c := range cancels {
					if i != res.index && c != nil {
						c()
					}
				}
				cancelsMu.Unlock()

				globalBalancer.RecordSuccess(dcID, res.domain)

				// Background drain for excess connections
				go func(winnerIndex int) {
					for i := 0; i < len(domains)-1; i++ {
						select {
						case extra := <-resultCh:
							if extra.index != winnerIndex && extra.ws != nil {
								_ = extra.ws.Close()
							}
						case <-time.After(2 * time.Second):
							return
						}
					}
				}(res.index)

				return res.ws, res.domain, nil
			}

			lastErr = res.err
			if IsDirectDomain(res.domain) {
				globalBalancer.RecordDirectFailure(dcID)
			}

			// If candidate failed early, trigger next candidate immediately
			if candidateIdx < len(domains) {
				launchCandidate(candidateIdx)
				candidateIdx++
			} else if activeCount == 0 {
				if lastErr == nil {
					lastErr = errors.New("all upstream dials failed")
				}
				return nil, "", lastErr
			}
		}
	}
}

