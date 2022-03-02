package proxy

import (
	"strings"
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
)

var ErrTip = map[byte]string{
	ERR:                 "proxy server error",
	ERR_RULE:            "proxy server rule refuse",
	ERR_NET:             "network unreachable",
	ERR_DST:             "host unreachable",
	ERR_DST_REFUSE:      "refused byte dst",
	ERR_TTL_EXPIRED:     "TTL expired",
	ERR_COMMAND_UNKNOWN: "command not supported",
	ERR_ADDR_TYPE:       "address type not supported",
	ERR_REPEAT_ID:       "repeat stream id",
	ERR_DIAL_TIMEOUT:    "dial dst i/o timeout",
	ERR_DST_RESET:       "reset by peer",
	ERR_CONN_TIMEOUT:    "forcibly closed by the remote host",
	ERR_FORCE_CLOSE:     "forcibly closed by proxy",
	ERR_ABORT:           "aborted by your host software",
}

var ErrCode = map[string]byte{
	"TTL":              ERR_TTL_EXPIRED,
	"refused":          ERR_DST_REFUSE,
	"support":          ERR_COMMAND_UNKNOWN,
	"network":          ERR_NET,
	"host unreachable": ERR_DST,
	"reset by peer":    ERR_DST_RESET,
	"forcibly closed":  ERR_CONN_TIMEOUT,
	"i/o timeout":      ERR_DIAL_TIMEOUT,
	"aborted by your":  ERR_ABORT,
}

func GetErrorCode(err error, defaultCode byte) byte {
	for k, v := range ErrCode {
		if strings.Contains(err.Error(), k) {
			return v
		}
	}
	return defaultCode
}

func GetErrorString(code byte, defaultStr string) string {
	tip := ErrTip[code]
	if tip == "" {
		tip = defaultStr
	}
	return tip
}
