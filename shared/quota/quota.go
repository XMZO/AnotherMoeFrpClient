// Package quota 是官方节点的限额：每个角色一份，个别用户可以覆盖部分字段。
// 面板计算出每个用户的有效限额下发给节点 agent，由 agent 在 frps 插件里执行（docs/node-agent.md）。
package quota

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"moefrp/shared/model"
)

// Quota 是一个用户在官方节点上能用的资源。
type Quota struct {
	MaxProxies int      `json:"maxProxies"` // 同时在线的代理数上限，0 表示不限
	Ports      string   `json:"ports"`      // 远程端口范围，例如 "10000-20000,30000"；空表示不限（以 frps 的 allowPorts 为准）
	Types      []string `json:"types"`      // 允许的代理类型，空表示全部
}

// Override 是单个用户对角色限额的覆盖，nil 字段表示沿用角色的设置。
type Override struct {
	MaxProxies *int      `json:"maxProxies,omitempty"`
	Ports      *string   `json:"ports,omitempty"`
	Types      *[]string `json:"types,omitempty"`
}

// Empty 判断覆盖是否为空。
func (o *Override) Empty() bool {
	return o == nil || (o.MaxProxies == nil && o.Ports == nil && o.Types == nil)
}

// Apply 返回应用了覆盖之后的限额。
func (q Quota) Apply(o *Override) Quota {
	if o == nil {
		return q
	}
	if o.MaxProxies != nil {
		q.MaxProxies = *o.MaxProxies
	}
	if o.Ports != nil {
		q.Ports = *o.Ports
	}
	if o.Types != nil {
		q.Types = slices.Clone(*o.Types)
	}
	return q
}

// MaxProxiesLimit 是 MaxProxies 能设的最大值。
const MaxProxiesLimit = 10000

// Validate 检查限额是否合法。
func (q Quota) Validate() error {
	if q.MaxProxies < 0 || q.MaxProxies > MaxProxiesLimit {
		return fmt.Errorf("代理数上限应在 0–%d 之间（0 表示不限）", MaxProxiesLimit)
	}
	if _, err := ParsePorts(q.Ports); err != nil {
		return err
	}
	for _, t := range q.Types {
		if !slices.Contains(model.ProxyTypes, t) {
			return fmt.Errorf("不支持的代理类型 %q", t)
		}
	}
	return nil
}

// Validate 检查覆盖里设置了的字段。
func (o *Override) Validate() error {
	if o == nil {
		return nil
	}
	return Quota{}.Apply(o).Validate()
}

// AllowsType 判断是否允许某种代理。
func (q Quota) AllowsType(t string) bool {
	return len(q.Types) == 0 || slices.Contains(q.Types, t)
}

// Range 是一段端口，包含两端。
type Range struct{ Lo, Hi int }

// Ranges 是若干段端口；为空表示不限。
type Ranges []Range

// ParsePorts 解析 "10000-20000, 30000" 这样的端口范围，空串表示不限。
func ParsePorts(s string) (Ranges, error) {
	var out Ranges
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b := a
		var err2 error
		if isRange {
			b, err2 = strconv.Atoi(strings.TrimSpace(hi))
		}
		if err1 != nil || err2 != nil || a < 1 || b > 65535 || a > b {
			return nil, fmt.Errorf("端口范围 %q 不正确，应类似 10000-20000,30000", part)
		}
		out = append(out, Range{a, b})
	}
	if len(out) > 100 {
		return nil, errors.New("端口范围最多 100 段")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Lo < out[j].Lo })
	return out, nil
}

// Contains 判断端口是否在范围内；范围为空时总是 true。
func (r Ranges) Contains(p int) bool {
	if len(r) == 0 {
		return true
	}
	for _, x := range r {
		if p >= x.Lo && p <= x.Hi {
			return true
		}
	}
	return false
}

// String 返回规范写法，例如 "10000-20000,30000"。
func (r Ranges) String() string {
	parts := make([]string, 0, len(r))
	for _, x := range r {
		if x.Lo == x.Hi {
			parts = append(parts, strconv.Itoa(x.Lo))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", x.Lo, x.Hi))
		}
	}
	return strings.Join(parts, ",")
}

// Roles 是各角色的默认限额。
type Roles struct {
	User  Quota `json:"user"`
	Admin Quota `json:"admin"`
}

// DefaultRoles 是没有设置过时的默认值：普通用户 5 个代理，管理员不限。
func DefaultRoles() Roles {
	return Roles{User: Quota{MaxProxies: 5}, Admin: Quota{}}
}

// For 返回某个角色的限额。
func (r Roles) For(role string) Quota {
	switch role {
	case model.RoleAdmin:
		return r.Admin
	default:
		return r.User
	}
}

// Validate 检查各角色的限额。
func (r Roles) Validate() error {
	for name, q := range map[string]Quota{"普通用户": r.User, "管理员": r.Admin} {
		if err := q.Validate(); err != nil {
			return fmt.Errorf("%s：%w", name, err)
		}
	}
	return nil
}
