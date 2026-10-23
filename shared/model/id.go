package model

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"time"
)

var (
	idMu     sync.Mutex
	idLastMS int64
	idSeq    uint16 // 同一毫秒内的递增计数，保证单调
)

// NewID 生成一个 UUIDv7（RFC 9562），用作配置、节点、代理、访客的稳定 ID。
//
// 同一毫秒内生成的 ID 用 12 位计数器保证递增；计数器用完时借用下一毫秒。
// 分享 ID 不能用它：带时间前缀，更容易被猜到（PLAN 7.6）。
func NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("model: crypto/rand failed: " + err.Error())
	}

	idMu.Lock()
	ms := time.Now().UnixMilli()
	if ms > idLastMS {
		idLastMS = ms
		idSeq = binary.BigEndian.Uint16(b[6:8]) & 0x07ff // 随机起点，留出一半空间递增
	} else {
		idSeq++
		if idSeq > 0x0fff {
			idLastMS++
			idSeq = 0
		}
		ms = idLastMS
	}
	seq := idSeq
	idMu.Unlock()

	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	b[6] = 0x70 | byte(seq>>8) // version 7
	b[7] = byte(seq)
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10

	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}
