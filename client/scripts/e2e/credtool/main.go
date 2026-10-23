// credtool 是端到端测试用的小工具：检查和清理凭据管理器里 MoeFrp 的凭据。
//
//	credtool list   <前缀>            列出目标名以前缀开头的凭据（JSON；只输出令牌长度和是否以 mfs_ 开头，不输出令牌）
//	credtool check  <目标名> <字符串>  凭据内容里是否包含该字符串（用来确认没有保存密码）
//	credtool delete <前缀>            删除目标名以前缀开头的全部凭据
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/danieljoos/wincred"
)

type item struct {
	Target     string `json:"target"`
	UserName   string `json:"userName"`
	BlobLen    int    `json:"blobLen"`
	TokenLike  bool   `json:"tokenLike"` // 以 mfs_ 开头
	Persist    string `json:"persist"`
	HasComment bool   `json:"hasComment"`
}

func list(prefix string) []*wincred.Credential {
	all, err := wincred.FilteredList(prefix + "*")
	if err != nil {
		return nil
	}
	return all
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: credtool list|check|delete ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "list":
		out := []item{}
		for _, c := range list(os.Args[2]) {
			blob := string(c.CredentialBlob)
			out = append(out, item{Target: c.TargetName, UserName: c.UserName, BlobLen: len(c.CredentialBlob),
				TokenLike: strings.HasPrefix(blob, "mfs_"), Persist: fmt.Sprint(c.Persist), HasComment: c.Comment != ""})
		}
		_ = json.NewEncoder(os.Stdout).Encode(out)
	case "check":
		if len(os.Args) < 4 {
			os.Exit(2)
		}
		g, err := wincred.GetGenericCredential(os.Args[2])
		if err != nil {
			fmt.Println(`{"exists":false}`)
			return
		}
		contains := strings.Contains(string(g.CredentialBlob), os.Args[3]) || strings.Contains(g.UserName, os.Args[3]) ||
			strings.Contains(g.Comment, os.Args[3])
		for _, a := range g.Attributes {
			if strings.Contains(string(a.Value), os.Args[3]) {
				contains = true
			}
		}
		fmt.Printf(`{"exists":true,"containsSecret":%v}`+"\n", contains)
	case "delete":
		n := 0
		for _, c := range list(os.Args[2]) {
			if g, err := wincred.GetGenericCredential(c.TargetName); err == nil && g.Delete() == nil {
				n++
			}
		}
		fmt.Printf(`{"deleted":%d}`+"\n", n)
	default:
		os.Exit(2)
	}
}
