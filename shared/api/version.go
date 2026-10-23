package api

import (
	"errors"
	"strconv"
	"strings"
)

// Version 是解析后的客户端版本号，格式为 [v]MAJOR[.MINOR[.PATCH]][-PRERELEASE][+BUILD]。
type Version struct {
	Major, Minor, Patch int
	Pre                 []string // 预发布标识，例如 beta.1 → ["beta", "1"]
}

var errBadVersion = errors.New("api: invalid version")

// ParseVersion 解析版本号。缺少的 MINOR、PATCH 视为 0；BUILD 部分被忽略。
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v Version
	core := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core = s[:i]
		pre := s[i+1:]
		if pre == "" {
			return Version{}, errBadVersion
		}
		v.Pre = strings.Split(pre, ".")
		for _, id := range v.Pre {
			if id == "" {
				return Version{}, errBadVersion
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return Version{}, errBadVersion
	}
	nums := [3]int{}
	for i, p := range parts {
		if p == "" || len(p) > 9 {
			return Version{}, errBadVersion
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, errBadVersion
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, nil
}

// Compare 返回 -1、0、1。规则同 semver：没有预发布标识的版本更大。
func (v Version) Compare(o Version) int {
	if c := cmpInt(v.Major, o.Major); c != 0 {
		return c
	}
	if c := cmpInt(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmpInt(v.Patch, o.Patch); c != 0 {
		return c
	}
	switch {
	case len(v.Pre) == 0 && len(o.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(o.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(o.Pre); i++ {
		if c := cmpPreID(v.Pre[i], o.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(v.Pre), len(o.Pre))
}

// CompareVersions 比较两个版本字符串，任意一个解析失败时返回错误。
func CompareVersions(a, b string) (int, error) {
	va, err := ParseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := ParseVersion(b)
	if err != nil {
		return 0, err
	}
	return va.Compare(vb), nil
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpPreID(a, b string) int {
	na, ea := strconv.Atoi(a)
	nb, eb := strconv.Atoi(b)
	switch {
	case ea == nil && eb == nil:
		return cmpInt(na, nb)
	case ea == nil: // 纯数字标识比字母标识小
		return -1
	case eb == nil:
		return 1
	}
	return strings.Compare(a, b)
}
