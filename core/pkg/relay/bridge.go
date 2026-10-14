package relay

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"tg-ws-proxy-core/pkg/crypto"
	"tg-ws-proxy-core/pkg/proto"
	"tg-ws-proxy-core/pkg/ws"
)

// BridgeWS manages bidirectional re-encrypting pipe between MTProto client and Telegram WebSocket.
func BridgeWS(
	ctx context.Context,
	client net.Conn,
	remoteWS *ws.RawWebSocket,
	cryptoCtx *crypto.CryptoContext,
	splitter *proto.MsgSplitter,
	stats *Stats,
	dcID int,
	isMedia bool,
) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// Keepalive ping ticker every 30s to prevent Cloudflare 100s idle timeout
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := remoteWS.Ping(); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Client -> WebSocket
	go func() {
		defer wg.Done()
		defer cancel()

		buf := make([]byte, 65536)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, err := client.Read(buf)
			if n > 0 {
				chunk := buf[:n]
				stats.AddBytesUp(n)

				plain := make([]byte, n)
				cryptoCtx.CltDec.XORKeyStream(plain, chunk)

				reEnc := make([]byte, n)
				cryptoCtx.TgEnc.XORKeyStream(reEnc, plain)

				if splitter != nil {
					parts := splitter.Split(reEnc, plain)
					if len(parts) > 1 {
						if err := remoteWS.SendBatch(parts); err != nil {
							return
						}
					} else if len(parts) == 1 {
						if err := remoteWS.Send(parts[0]); err != nil {
							return
						}
					}
				} else {
					if err := remoteWS.Send(reEnc); err != nil {
						return
					}
				}
			}

			if err != nil {
				if err != io.EOF {
					// Client closed or reset
				}
				if splitter != nil {
					tail := splitter.Flush()
					if len(tail) > 0 {
						_ = remoteWS.Send(tail[0])
					}
				}
				return
			}
		}
	}()

	// WebSocket -> Client
	go func() {
		defer wg.Done()
		defer cancel()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			data, err := remoteWS.Recv()
			if err != nil {
				return
			}

			n := len(data)
			stats.AddBytesDown(n)

			plain := make([]byte, n)
			cryptoCtx.TgDec.XORKeyStream(plain, data)

			reEnc := make([]byte, n)
			cryptoCtx.CltEnc.XORKeyStream(reEnc, plain)

			if _, err := client.Write(reEnc); err != nil {
				return
			}
		}
	}()

	<-ctx.Done()
	_ = client.Close()
	_ = remoteWS.Close()
	wg.Wait()
}

// FormatBytes formats byte counts into human-readable B, KB, or MB units.
func FormatBytes(b uint64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	} else if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(b)/1024.0)
	}
	return fmt.Sprintf("%.2f MB", float64(b)/(1024.0*1024.0))
}
