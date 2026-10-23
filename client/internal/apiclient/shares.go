package apiclient

import (
	"context"
	"net/http"

	"moefrp/shared/api"
)

// CreateShare：POST /api/v1/shares。
func (c *Client) CreateShare(ctx context.Context, req api.CreateShareRequest) (api.Share, error) {
	req.NodeIDs, req.ProxyIDs = nonNil(req.NodeIDs), nonNil(req.ProxyIDs)
	var out api.Share
	_, _, err := c.do(ctx, http.MethodPost, api.RouteShares, nil, req, &out)
	return out, err
}

// ListShares：GET /api/v1/shares。
func (c *Client) ListShares(ctx context.Context) ([]api.Share, error) {
	var out api.ListSharesResponse
	_, _, err := c.do(ctx, http.MethodGet, api.RouteShares, nil, nil, &out)
	return nonNil(out.Shares), err
}

// UpdateShare：PATCH /api/v1/shares/{id}。
func (c *Client) UpdateShare(ctx context.Context, id string, req api.UpdateShareRequest) (api.Share, error) {
	var out api.Share
	_, _, err := c.do(ctx, http.MethodPatch, api.SharePath(id), nil, req, &out)
	return out, err
}

// RepublishShare：POST /api/v1/shares/{id}/republish。
func (c *Client) RepublishShare(ctx context.Context, id string) (api.Share, error) {
	var out api.Share
	_, _, err := c.do(ctx, http.MethodPost, api.ShareRepublishPath(id), nil, nil, &out)
	return out, err
}

// RevokeShare：DELETE /api/v1/shares/{id}。
func (c *Client) RevokeShare(ctx context.Context, id string) error {
	_, _, err := c.do(ctx, http.MethodDelete, api.SharePath(id), nil, nil, nil)
	return err
}

// SharePreview：GET /api/v1/shares/{id}/preview。
func (c *Client) SharePreview(ctx context.Context, id string) (api.SharePreview, error) {
	var out api.SharePreview
	_, _, err := c.do(ctx, http.MethodGet, api.SharePreviewPath(id), nil, nil, &out)
	return out, err
}

// Subscribe：POST /api/v1/subscriptions。
func (c *Client) Subscribe(ctx context.Context, req api.CreateSubscriptionRequest) (api.SubscriptionSummary, error) {
	req.Params.Normalize()
	var out api.SubscriptionSummary
	_, _, err := c.do(ctx, http.MethodPost, api.RouteSubscriptions, nil, req, &out)
	return out, err
}

// UpdateSubscription：PUT /api/v1/subscriptions/{id}。
func (c *Client) UpdateSubscription(ctx context.Context, id string, req api.UpdateSubscriptionRequest) (api.SubscriptionSummary, error) {
	req.Params.Normalize()
	var out api.SubscriptionSummary
	_, _, err := c.do(ctx, http.MethodPut, api.SubscriptionPath(id), nil, req, &out)
	return out, err
}

// Unsubscribe：DELETE /api/v1/subscriptions/{id}。
func (c *Client) Unsubscribe(ctx context.Context, id string) error {
	_, _, err := c.do(ctx, http.MethodDelete, api.SubscriptionPath(id), nil, nil, nil)
	return err
}

// SubscriptionRuntime：GET /api/v1/subscriptions/{id}/runtime。
func (c *Client) SubscriptionRuntime(ctx context.Context, id string) (api.SubscriptionRuntime, error) {
	var out api.SubscriptionRuntime
	_, _, err := c.do(ctx, http.MethodGet, api.SubscriptionRuntimePath(id), nil, nil, &out)
	out.Data.Normalize()
	return out, err
}
