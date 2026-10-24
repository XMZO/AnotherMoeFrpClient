// Package vault 是节点的本地加密存储（docs/node-agent.md 第 4 节）：frps 配置、授权表、准入状态都存在这里，
// 磁盘上没有明文。
//
// 两层密钥：
//   - 存储密钥：面板下发（agentproto.StoreLease），= HMAC(面板主密钥, 节点 ID + 机器指纹)，加密各个数据文件；
//   - 封存密钥：本机算出，= SHA-256(机器指纹 + agent 令牌)，加密"存储密钥 + 租约 + 时间高水位"，存在 vault.seal。
//
// 租约：面板每次连上都续约（默认 7 天）。节点重启时面板离线，租约没过期就照常解开、启动 frps；过期后保持锁定，
// 等面板连上重新下发。时间一律是 Unix 秒（UTC，和时区无关），面板发来的到期时间按面板时间和本机时间的差换算，
// 本机时钟不准也不影响租约长短。高水位记下见过的最晚本机时间，把系统时间往回调绕过租约会被发现。
//
// 能防：配置文件被拷走、备份泄露、换了机器（机器指纹不同，解不开）、租约过期后离线使用。
// 防不了：这台机器上有 root 的人（他能读到机器指纹和令牌，也能读进程内存）。
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"moefrp/agent/internal/agentcfg"
)

const (
	sealFile = "vault.seal"
	encExt   = ".enc"
	// rollbackSkew：本机时间比高水位早这么多以内不算回调（NTP 校时之类的小幅调整）。
	rollbackSkew = 10 * time.Minute
)

// ErrLocked 表示还没有可用的存储密钥（没收到面板下发、租约过期、封存文件解不开）。
var ErrLocked = errors.New("本地加密存储还锁着，等面板连上后下发存储密钥")

// Vault 见包注释。所有方法可以并发调用。
type Vault struct {
	dir     string
	sealKey []byte
	now     func() time.Time

	mu     sync.Mutex
	key    []byte // 存储密钥，nil 表示锁着
	until  int64  // 租约到期（本机时间）
	high   int64  // 见过的最晚本机时间
	reason string // 锁着的原因
}

type sealed struct {
	Key   []byte `json:"key"`
	Until int64  `json:"until"`
	High  int64  `json:"high"`
}

// Open 打开 dir 里的存储并尝试用封存文件解锁。解不开时保持锁定，原因见 Reason。
func Open(dir, machineID, token string, now func() time.Time) *Vault {
	if now == nil {
		now = time.Now
	}
	sum := sha256.Sum256([]byte("moefrp-vault-seal:" + machineID + ":" + token))
	v := &Vault{dir: dir, sealKey: sum[:], now: now, reason: "还没收到面板下发的存储密钥"}
	b, err := os.ReadFile(filepath.Join(dir, sealFile))
	if errors.Is(err, os.ErrNotExist) {
		return v
	}
	plain, err := open(v.sealKey, b)
	if err != nil {
		v.reason = "封存文件解不开（机器指纹或 agent 令牌变了）"
		return v
	}
	var s sealed
	if json.Unmarshal(plain, &s) != nil || len(s.Key) != 32 {
		v.reason = "封存文件损坏"
		return v
	}
	t := now().Unix()
	v.high, v.until = s.High, s.Until
	switch {
	case t < s.High-int64(rollbackSkew/time.Second):
		v.reason = fmt.Sprintf("系统时间（%s）比上次运行时（%s）还早，可能被往回调过", fmtTime(t), fmtTime(s.High))
	case t > s.Until:
		v.reason = fmt.Sprintf("存储租约在 %s 到期了", fmtTime(s.Until))
	default:
		v.key, v.reason = s.Key, ""
		v.high = max(v.high, t)
		_ = v.saveSealLocked()
	}
	return v
}

func fmtTime(unix int64) string { return time.Unix(unix, 0).UTC().Format("2006-01-02 15:04 UTC") }

// Unlocked 判断存储能不能读写。
func (v *Vault) Unlocked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.key != nil
}

// Reason 返回锁着的原因，解锁时为空。
func (v *Vault) Reason() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.reason
}

// LeaseUntil 返回租约到期时间（本机时间），锁着时为零值。
func (v *Vault) LeaseUntil() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return time.Time{}
	}
	return time.Unix(v.until, 0)
}

// SetLease 换上面板下发的存储密钥和租约，返回这次是否从锁着变成解锁。
// 密钥变了（面板轮换了主密钥）时，用新密钥重新加密已有的文件；锁着时留下的、新密钥解不开的旧文件删掉
// （它们是用作废的密钥加密的，面板会重新下发内容）。
func (v *Vault) SetLease(key []byte, leaseUntil, serverTime int64) (unlocked bool, err error) {
	if len(key) != 32 {
		return false, errors.New("存储密钥长度不对")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now().Unix()
	old := v.key
	if old == nil || string(old) != string(key) {
		names, err := v.listLocked()
		if err != nil {
			return false, err
		}
		for _, name := range names {
			path := filepath.Join(v.dir, name+encExt)
			b, err := os.ReadFile(path)
			if err != nil {
				return false, err
			}
			var plain []byte
			if old != nil {
				plain, err = open(old, b)
			} else {
				plain, err = open(key, b)
			}
			if err != nil {
				_ = os.Remove(path)
				continue
			}
			c, err := seal(key, plain)
			if err != nil {
				return false, err
			}
			if err := agentcfg.WriteFileAtomic(path, c, 0o600); err != nil {
				return false, err
			}
		}
	}
	v.key = append([]byte(nil), key...)
	v.until = leaseUntil + (now - serverTime) // 换算到本机时间
	v.high = max(v.high, now)
	v.reason = ""
	if err := v.saveSealLocked(); err != nil {
		return false, err
	}
	return old == nil, nil
}

// Touch 推高时间高水位（定期调用）。
func (v *Vault) Touch() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return
	}
	if t := v.now().Unix(); t > v.high {
		v.high = t
		_ = v.saveSealLocked()
	}
}

// Read 读出一个文件。不存在时返回 os.ErrNotExist，锁着时返回 ErrLocked。
func (v *Vault) Read(name string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return nil, ErrLocked
	}
	b, err := os.ReadFile(filepath.Join(v.dir, name+encExt))
	if err != nil {
		return nil, err
	}
	plain, err := open(v.key, b)
	if err != nil {
		return nil, fmt.Errorf("%s 解不开：%w", name, err)
	}
	return plain, nil
}

// Write 加密写入一个文件。锁着时返回 ErrLocked。
func (v *Vault) Write(name string, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return ErrLocked
	}
	c, err := seal(v.key, data)
	if err != nil {
		return err
	}
	return agentcfg.WriteFileAtomic(filepath.Join(v.dir, name+encExt), c, 0o600)
}

func (v *Vault) saveSealLocked() error {
	b, _ := json.Marshal(sealed{Key: v.key, Until: v.until, High: v.high})
	c, err := seal(v.sealKey, b)
	if err != nil {
		return err
	}
	return agentcfg.WriteFileAtomic(filepath.Join(v.dir, sealFile), c, 0o600)
}

func (v *Vault) listLocked() ([]string, error) {
	es, err := os.ReadDir(v.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if name, ok := strings.CutSuffix(e.Name(), encExt); ok && !e.IsDir() {
			out = append(out, name)
		}
	}
	return out, nil
}

// seal 用 AES-256-GCM 加密：随机 nonce 放在密文前面。
func seal(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func open(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("密文太短")
	}
	return gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
}
