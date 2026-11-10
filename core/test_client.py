import hashlib
import hmac
import logging
import os
import socket
import struct
import time

logging.basicConfig(level=logging.INFO, format="%(message)s")

SECRET = bytes.fromhex('d41d8cd98f00b204e9800998ecf8427e')

def build_client_hello():
    session_id = os.urandom(32)
    body = bytearray(517)
    body[0] = 0x16
    body[1:3] = b'\x03\x01'
    struct.pack_into('>H', body, 3, len(body) - 5)
    body[5] = 0x01
    body[43] = 0x20
    body[44:76] = session_id

    digest = hmac.new(SECRET, bytes(body), hashlib.sha256).digest()
    client_random = bytearray(digest[:32])
    ts_bytes = struct.pack('<I', int(time.time()))
    for i in range(4):
        client_random[28 + i] = digest[28 + i] ^ ts_bytes[i]

    body[11:43] = client_random
    return bytes(body), client_random, session_id

s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.connect(('127.0.0.1', 2443))
ch, cr, sid = build_client_hello()
logging.info("Sending ClientHello (%d bytes)...", len(ch))
s.sendall(ch)

resp = s.recv(4096)
logging.info("Received ServerHello (%d bytes)", len(resp))

zeroed = bytearray(resp)
zeroed[11:43] = b'\x00' * 32
expected = hmac.new(SECRET, cr + bytes(zeroed), hashlib.sha256).digest()
server_random = resp[11:43]

logging.info("Server random matches expected: %s", server_random == expected)
s.close()
