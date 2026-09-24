package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

func (c *Client) GetMonitor(ctx context.Context, id string) (*Monitor, error) {
	var m Monitor
	if err := c.do(ctx, http.MethodGet, "/monitors/"+url.PathEscape(id), nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func (c *Client) ListMonitors(ctx context.Context) ([]Monitor, error) {
	var out struct {
		Monitors []Monitor `json:"monitors"`
	}
	if err := c.do(ctx, http.MethodGet, "/monitors", nil, &out); err != nil {
		return nil, err
	}
	return out.Monitors, nil
}

func (c *Client) CreateMonitor(ctx context.Context, w *MonitorWrite) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/monitors", w, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("create monitor: response had no id")
	}
	return out.ID, nil
}

func (c *Client) UpdateMonitor(ctx context.Context, id string, w *MonitorWrite) error {
	return c.do(ctx, http.MethodPatch, "/monitors/"+url.PathEscape(id), w, nil)
}

// DeleteMonitor answers 204 (channels answer 200 {ok:true}); both are plain success here.
func (c *Client) DeleteMonitor(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/monitors/"+url.PathEscape(id), nil, nil)
}

func (c *Client) GetChannel(ctx context.Context, id string) (*Channel, error) {
	var ch Channel
	if err := c.do(ctx, http.MethodGet, "/channels/"+url.PathEscape(id), nil, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

func (c *Client) ListChannels(ctx context.Context) ([]Channel, error) {
	var out struct {
		Channels []Channel `json:"channels"`
	}
	if err := c.do(ctx, http.MethodGet, "/channels", nil, &out); err != nil {
		return nil, err
	}
	return out.Channels, nil
}

func (c *Client) CreateChannel(ctx context.Context, in ChannelCreate) (*ChannelCreateResult, error) {
	var out ChannelCreateResult
	if err := c.do(ctx, http.MethodPost, "/channels", in, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, errors.New("create channel: response had no id")
	}
	return &out, nil
}

func (c *Client) UpdateChannel(ctx context.Context, id string, p ChannelPatch) error {
	return c.do(ctx, http.MethodPatch, "/channels/"+url.PathEscape(id), p, nil)
}

func (c *Client) DeleteChannel(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/channels/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	var out struct {
		Agents []Agent `json:"agents"`
	}
	if err := c.do(ctx, http.MethodGet, "/agents", nil, &out); err != nil {
		return nil, err
	}
	return out.Agents, nil
}
