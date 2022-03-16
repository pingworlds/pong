package proxy

import (
	"net"
	"os"
	"strings"
	"syscall"
)

const (
	//socks5 error code
	ERR                 byte = 1
	ERR_RULE            byte = 2
	ERR_NET             byte = 3
	ERR_DST             byte = 4
	ERR_DST_REFUSE      byte = 5
	ERR_TTL_EXPIRED     byte = 6
	ERR_COMMAND_UNKNOWN byte = 7
	ERR_ADDR_TYPE       byte = 8

	ERR_REPEAT_ID    byte = 9
	ERR_CONN_TIMEOUT byte = 10
	ERR_DIAL_TIMEOUT byte = 11
	ERR_DST_RESET    byte = 12
	ERR_FORCE_CLOSE  byte = 13
	ERR_ABORT        byte = 14

	ERR_TIMEOUT         byte = 16
	ERR_INVALID_ADDR    byte = 17
	ERR_UNKNOWN_NETWORK byte = 18
	ERR_DNS             byte = 19
	ERR_DNS_CONFIG      byte = 20
	ERR_REFUSE          byte = 21
	ERR_ADDR            byte = 22
	ERR_SRC_ABORT       byte = 23
	ERR_DST_TIMEOUT     byte = 24
	ERR_DST_DOWN        byte = 25
	ERR_DST_UNREACH     byte = 26
	ERR_RESET           byte = 27
	ERR_READ_TIMEOUT    byte = 28
	ERR_CLOSED          byte = 29
	ERR_DST_ABORT       byte = 30
	ERR_UNKNOWN         byte = 50
)

var ErrTip = map[byte]string{
	ERR:                 "proxy server error",
	ERR_RULE:            "proxy server rule refuse",
	ERR_NET:             "use of closed network connection",
	ERR_DST_UNREACH:     "The server is unreachable",
	ERR_DST_REFUSE:      "The remote host actively refused the attempt to connect to it",
	ERR_TTL_EXPIRED:     "TTL expired",
	ERR_COMMAND_UNKNOWN: "command not supported",
	ERR_ADDR_TYPE:       "address type not supported",
	ERR_REPEAT_ID:       "repeat stream id",
	ERR_DIAL_TIMEOUT:    "dial dst i/o timeout",
	ERR_DST_RESET:       "reset by peer",
	ERR_CONN_TIMEOUT:    "forcibly closed by the remote host",
	ERR_FORCE_CLOSE:     "forcibly closed by proxy",
	ERR_ABORT:           "aborted by your host software",

	ERR_DST_TIMEOUT:     "The connection failed due to an error or timeout",
	ERR_TIMEOUT:         "net timeout",
	ERR_INVALID_ADDR:    "invalid error",
	ERR_UNKNOWN_NETWORK: "unknown network",
	ERR_DNS:             "dns error",
	ERR_DNS_CONFIG:      "dns config error",
	ERR_REFUSE:          "connection refuse",
	ERR_RESET:           "reset by peer",
	ERR_ADDR:            "addr error",
	ERR_SRC_ABORT:       "aborted by the software in your host machine, possibly because of timeout or protocol error",
	ERR_DST_ABORT:       "forcibly closed by the remote host",
	ERR_DST_DOWN:        "The server is temporarily or permanently unreachable",
	ERR_READ_TIMEOUT:    "read timeout",
	ERR_CLOSED:          "use of closed network connection",
	ERR_UNKNOWN:         "unknown error",
}

var ErrTags = map[string]byte{
	"established connection was aborted":            ERR_SRC_ABORT,
	"connection was forcibly closed by the remote ": ERR_DST_ABORT,
	"connected host has failed to respond":          ERR_DST_TIMEOUT,
	"broken pipe":                                   ERR_RESET,
}

func GetErrorCodeFromTags(err error) (code byte) {
	str := err.Error()
	for k, v := range ErrTags {
		if strings.Contains(str, k) {
			return v
		}
	}

	if os.IsTimeout(err) {
		return ERR_TIMEOUT
	}
	return ERR_UNKNOWN
}

func GetErrorCode(err error) (code byte) {
	if err == net.ErrClosed {
		code = ERR_TTL_EXPIRED
		return
	}

	netErr, ok := err.(net.Error)
	if !ok {
		code = ERR
		return
	}

	opErr, ok := netErr.(*net.OpError)
	if !ok {
		code = ERR_NET
		return
	}

	switch t := opErr.Err.(type) {
	case *net.DNSError:
		code = ERR_DNS
	case *net.InvalidAddrError:
		code = ERR_INVALID_ADDR
	case *net.UnknownNetworkError:
		code = ERR_UNKNOWN_NETWORK
	case *net.AddrError:
		code = ERR_ADDR
	case *net.DNSConfigError:
		code = ERR_DNS_CONFIG

	case *os.SyscallError:
		if errno, ok := t.Err.(syscall.Errno); ok {
			switch errno {
			case 0x274d, syscall.ECONNREFUSED:
				code = ERR_DST_REFUSE
			case 0x10053, syscall.ECONNABORTED:
				code = ERR_SRC_ABORT
			case 0x10054, syscall.ECONNRESET:
				code = ERR_DST_RESET
			case 0x10060, syscall.ETIMEDOUT:
				code = ERR_DST_TIMEOUT
			case 0x10061:
				code = ERR_DST_REFUSE
			case 0x10064, syscall.EHOSTDOWN:
				code = ERR_DST_DOWN
			case 0x10065, syscall.EHOSTUNREACH:
				code = ERR_DST_UNREACH
			}
		}
	}

	if code == 0 {
		code = GetErrorCodeFromTags(err)
	}

	return
}
