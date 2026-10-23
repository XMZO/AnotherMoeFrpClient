package httpapi

import (
	"net/http"

	"moefrp/shared/api"
)

// POST /api/v1/shares
func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	var req api.CreateShareRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	sh, err := s.shares.Create(r.Context(), principalFrom(r).User.ID, req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", api.SharePath(sh.ID))
	writeJSON(w, http.StatusCreated, sh)
}

// GET /api/v1/shares
func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	list, err := s.shares.List(r.Context(), principalFrom(r).User.ID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, api.ListSharesResponse{Shares: list})
}

// PATCH /api/v1/shares/{id}
func (s *Server) handleUpdateShare(w http.ResponseWriter, r *http.Request) {
	var req api.UpdateShareRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	sh, err := s.shares.Update(r.Context(), principalFrom(r).User.ID, r.PathValue("id"), req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

// POST /api/v1/shares/{id}/republish
func (s *Server) handleRepublishShare(w http.ResponseWriter, r *http.Request) {
	sh, err := s.shares.Republish(r.Context(), principalFrom(r).User.ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sh)
}

// DELETE /api/v1/shares/{id}：撤销（软删除），可以重复调用。
func (s *Server) handleRevokeShare(w http.ResponseWriter, r *http.Request) {
	if err := s.shares.Revoke(r.Context(), principalFrom(r).User.ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/shares/{id}/preview
func (s *Server) handleSharePreview(w http.ResponseWriter, r *http.Request) {
	pv, err := s.shares.Preview(r.Context(), principalFrom(r).User.ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

// POST /api/v1/subscriptions
func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	var req api.CreateSubscriptionRequest
	if err := decodeJSON(r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	sub, err := s.shares.Subscribe(r.Context(), principalFrom(r).User.ID, req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Location", api.SubscriptionPath(sub.ID))
	writeJSON(w, http.StatusCreated, sub)
}

// PUT /api/v1/subscriptions/{id}
func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	var req api.UpdateSubscriptionRequest
	if err := decodeJSONRequired(r, &req, "baseRevision", "params"); err != nil {
		s.writeError(w, r, err)
		return
	}
	sub, err := s.shares.UpdateSubscription(r.Context(), principalFrom(r).User.ID, r.PathValue("id"), req)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sub)
}

// DELETE /api/v1/subscriptions/{id}
func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if err := s.shares.Unsubscribe(r.Context(), principalFrom(r).User.ID, r.PathValue("id")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/subscriptions/{id}/runtime
func (s *Server) handleSubscriptionRuntime(w http.ResponseWriter, r *http.Request) {
	rt, err := s.shares.Runtime(r.Context(), principalFrom(r).User.ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rt)
}
