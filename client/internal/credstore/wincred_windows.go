package credstore

import (
	"errors"

	"github.com/danieljoos/wincred"
)

// WinCred 是 Windows 凭据管理器里的一条"普通凭据"（CRED_TYPE_GENERIC）。
type WinCred struct {
	Target string
}

// NewWinCred 返回指定目标名的凭据，target 为空时用 DefaultTarget。
func NewWinCred(target string) *WinCred {
	if target == "" {
		target = DefaultTarget
	}
	return &WinCred{Target: target}
}

func (w *WinCred) Load() (Cred, bool, error) {
	g, err := wincred.GetGenericCredential(w.Target)
	if errors.Is(err, wincred.ErrElementNotFound) {
		return Cred{}, false, nil
	}
	if err != nil {
		return Cred{}, false, err
	}
	if len(g.CredentialBlob) == 0 {
		return Cred{}, false, nil
	}
	return Cred{Nickname: g.UserName, Token: string(g.CredentialBlob)}, true, nil
}

func (w *WinCred) Save(c Cred) error {
	if c.Token == "" {
		return ErrEmptyToken
	}
	g := wincred.NewGenericCredential(w.Target)
	g.UserName = c.Nickname
	g.Comment = "MoeFrp session token (not a password)"
	g.CredentialBlob = []byte(c.Token)
	// 只保存在本机，不随漫游配置文件同步到别的电脑
	g.Persist = wincred.PersistLocalMachine
	return g.Write()
}

func (w *WinCred) Delete() error {
	g, err := wincred.GetGenericCredential(w.Target)
	if errors.Is(err, wincred.ErrElementNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := g.Delete(); err != nil && !errors.Is(err, wincred.ErrElementNotFound) {
		return err
	}
	return nil
}
