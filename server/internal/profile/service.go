// Package profile 实现配置的增删改查和用户的 stateVersion（PLAN 6.6、7.5）。
//
// 每次改动配置都在同一个事务里让 users.state_version 加 1，
// 客户端用它做 GET /api/v1/state 的 ETag。
package profile

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"moefrp/server/internal/share"
	"moefrp/server/internal/store"
	"moefrp/shared/api"
	"moefrp/shared/apierr"
	"moefrp/shared/model"
	"moefrp/shared/validate"
)

// Service 是配置服务。
type Service struct {
	st  *store.Store
	now func() time.Time
}

// New 创建配置服务。now 为 nil 时用 time.Now。
func New(st *store.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{st: st, now: now}
}

// State 是 GET /api/v1/state 需要的数据。
type State struct {
	User          store.User
	Profiles      []model.Profile
	Subscriptions []api.SubscriptionSummary
}

// StateVersion 只查 stateVersion，用于 If-None-Match 命中时直接返回 304。
func (s *Service) StateVersion(ctx context.Context, userID string) (int64, error) {
	u, err := store.GetUser(ctx, s.st.DB(), userID)
	if err != nil {
		return 0, err
	}
	return u.StateVersion, nil
}

// State 在同一个只读快照里读出用户（含 stateVersion）和全部配置，保证两者一致。
func (s *Service) State(ctx context.Context, userID string) (State, error) {
	var out State
	err := s.st.ReadTx(ctx, func(tx *sql.Tx) error {
		u, err := store.GetUser(ctx, tx, userID)
		if err != nil {
			return err
		}
		rows, err := store.ListProfiles(ctx, tx, userID)
		if err != nil {
			return err
		}
		out.User = u
		out.Profiles = make([]model.Profile, 0, len(rows))
		for _, r := range rows {
			p, err := toModel(r)
			if err != nil {
				return err
			}
			out.Profiles = append(out.Profiles, p)
		}
		out.Subscriptions, err = share.Summaries(ctx, tx, userID, s.now().Unix())
		return err
	})
	return out, err
}

// Get 返回用户的一个配置。不存在或不属于该用户时返回 not_found。
func (s *Service) Get(ctx context.Context, userID, id string) (model.Profile, error) {
	r, err := store.GetOwnedProfile(ctx, s.st.DB(), userID, id)
	if errors.Is(err, store.ErrNotFound) {
		return model.Profile{}, apierr.New(apierr.NotFound)
	}
	if err != nil {
		return model.Profile{}, err
	}
	return toModel(r)
}

// Create 新建配置。req.ID 为空时由服务端生成；节点、代理、访客缺 ID 时补上。
//
// 客户端带来的 ID 已经属于该用户时返回 409 revision_conflict（details.current 为云端版本），
// 方便客户端在"请求已成功、响应丢失"后重试时直接对齐；属于别人时返回 validation_failed。
func (s *Service) Create(ctx context.Context, userID string, req api.CreateProfileRequest) (model.Profile, error) {
	id := req.ID
	if id == "" {
		id = model.NewID()
	}
	data := req.Data()
	data.EnsureIDs()
	data.Normalize()
	if err := checkProfile(id, req.Name, data); err != nil {
		return model.Profile{}, err
	}
	blob, err := json.Marshal(data)
	if err != nil {
		return model.Profile{}, err
	}

	now := s.now().Unix()
	row := store.ProfileRow{
		ID: id, OwnerID: userID, Name: req.Name, Data: string(blob),
		SchemaVersion: store.CurrentProfileSchema, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		if existing, err := store.GetProfile(ctx, tx, id); err == nil {
			if existing.OwnerID == userID {
				return conflict(existing)
			}
			return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, validate.Errors{{
				Field: "id", Reason: validate.ReasonDuplicate, Message: "ID 已被占用",
			}})
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		n, err := store.CountProfiles(ctx, tx, userID)
		if err != nil {
			return err
		}
		if n >= validate.MaxProfilesPerUser {
			return apierr.New(apierr.LimitExceeded).With(apierr.DetailLimit, validate.MaxProfilesPerUser)
		}
		if err := store.InsertProfile(ctx, tx, row); err != nil {
			return err
		}
		_, err = store.BumpStateVersion(ctx, tx, userID)
		return err
	})
	if err != nil {
		return model.Profile{}, err
	}
	return toModel(row)
}

// Update 覆盖配置的名称和内容。baseRevision 与云端不一致时返回 409 revision_conflict，
// details.current 是云端当前的完整配置，供客户端弹窗比较（PLAN 6.6）。
func (s *Service) Update(ctx context.Context, userID, id string, req api.UpdateProfileRequest) (model.Profile, error) {
	if req.BaseRevision <= 0 {
		return model.Profile{}, apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, validate.Errors{{
			Field: "baseRevision", Reason: validate.ReasonRequired, Message: "baseRevision 必填，是编辑时基于的云端版本",
		}})
	}
	data := req.Data()
	data.EnsureIDs()
	data.Normalize()
	if err := checkProfile(id, req.Name, data); err != nil {
		return model.Profile{}, err
	}
	blob, err := json.Marshal(data)
	if err != nil {
		return model.Profile{}, err
	}

	now := s.now().Unix()
	var updated store.ProfileRow
	err = s.st.Tx(ctx, func(tx *sql.Tx) error {
		row, ok, err := store.UpdateProfileIfRevision(ctx, tx, userID, id, req.BaseRevision, req.Name, string(blob), now)
		if err != nil {
			return err
		}
		if !ok {
			current, err := store.GetOwnedProfile(ctx, tx, userID, id)
			if errors.Is(err, store.ErrNotFound) {
				return apierr.New(apierr.NotFound)
			}
			if err != nil {
				return err
			}
			return conflict(current)
		}
		updated = row
		_, err = store.BumpStateVersion(ctx, tx, userID)
		return err
	})
	if err != nil {
		return model.Profile{}, err
	}
	return toModel(updated)
}

// Delete 删除配置。
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	return s.st.Tx(ctx, func(tx *sql.Tx) error {
		if err := store.DeleteProfile(ctx, tx, userID, id); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return apierr.New(apierr.NotFound)
			}
			return err
		}
		_, err := store.BumpStateVersion(ctx, tx, userID)
		return err
	})
}

func checkProfile(id, name string, data model.ProfileData) error {
	p := model.Profile{ID: id, Name: name, Nodes: data.Nodes, Proxies: data.Proxies, Visitors: data.Visitors}
	if err := validate.Profile(p); err != nil {
		var es validate.Errors
		if errors.As(err, &es) {
			return apierr.New(apierr.ValidationFailed).With(apierr.DetailFields, es)
		}
		return err
	}
	return nil
}

func conflict(current store.ProfileRow) error {
	p, err := toModel(current)
	if err != nil {
		return err
	}
	return apierr.New(apierr.RevisionConflict).With(apierr.DetailCurrent, p)
}

func toModel(r store.ProfileRow) (model.Profile, error) {
	var d model.ProfileData
	if err := json.Unmarshal([]byte(r.Data), &d); err != nil {
		return model.Profile{}, fmt.Errorf("profile %s: decode data: %w", r.ID, err)
	}
	d.Normalize()
	return model.Profile{
		ID:        r.ID,
		Name:      r.Name,
		Nodes:     d.Nodes,
		Proxies:   d.Proxies,
		Visitors:  d.Visitors,
		Revision:  r.Revision,
		UpdatedAt: time.Unix(r.UpdatedAt, 0).UTC(),
	}, nil
}
