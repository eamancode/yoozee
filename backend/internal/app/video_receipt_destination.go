package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"time"

	"yingce/backend/internal/model"
)

const videoReceiptOriginKey = "_videoReceiptOrigin"

type videoReceiptOriginStamp struct {
	ChannelID string `json:"channelId"`
	RouteRun  int    `json:"routeRun"`
	MAC       string `json:"mac"`
}

func videoReceiptOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", failedPrecondition("无法核实原视频请求的上游地址")
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port), nil
}

func (s *Service) videoReceiptOriginMAC(task *model.Task, channelID, origin string) ([]byte, error) {
	key, err := s.settingsEncryptionKey()
	if err != nil {
		return nil, err
	}
	// Bind the fallback proof to this task/run; an older client's arbitrary
	// JSON or a stamp copied from another order cannot certify a destination.
	binding, err := json.Marshal([]any{videoReceiptOriginKey, task.UserID, task.ID, task.RouteRun, channelID, origin})
	if err != nil {
		return nil, err
	}
	h := hmac.New(sha256.New, key)
	h.Write(binding)
	return h.Sum(nil), nil
}

func (s *Service) stampVideoReceiptDestination(task *model.Task, input map[string]any) error {
	delete(input, videoReceiptOriginKey)
	if !isVideoTaskType(task.Type) {
		return nil
	}
	config, _ := input["config"].(map[string]any)
	id := stringValue(config["channelId"])
	if id == "" {
		return nil
	}
	channel, err := s.repo.SystemChannel(id)
	if err != nil {
		return err
	}
	origin, err := videoReceiptOrigin(channel.BaseURL)
	if err != nil {
		return err
	}
	proof, err := s.videoReceiptOriginMAC(task, id, origin)
	if err != nil {
		return err
	}
	input[videoReceiptOriginKey] = videoReceiptOriginStamp{ChannelID: id, RouteRun: task.RouteRun, MAC: hex.EncodeToString(proof)}
	return nil
}

// Call only after the original job, owner, attempt, model and channel have
// been reconciled. The actual creation receipt takes precedence over admission.
func (s *Service) validateVideoReceiptDestination(task *model.Task, channel *model.ModelChannel, jobID string, since time.Time) error {
	current, err := videoReceiptOrigin(channel.BaseURL)
	if err != nil {
		return err
	}
	if since.IsZero() {
		if task.RouteRun > 1 {
			return failedPrecondition("缺少本次尝试的起始时间，无法核实原视频地址")
		}
		since = task.CreatedAt
	}
	urls, err := s.repo.VideoReceiptCreateURLs(task.UserID, task.ID, task.BillingOrderID, channel.ID, jobID, since)
	if err != nil {
		return err
	}
	mismatch := failedPrecondition("渠道当前地址与原视频回执不一致，请核对原请求的上游地址")
	for _, raw := range urls {
		recorded, err := videoReceiptOrigin(raw)
		if err != nil || recorded != current {
			return mismatch
		}
	}
	if len(urls) > 0 {
		return nil
	}
	var saved struct {
		Stamp *videoReceiptOriginStamp `json:"_videoReceiptOrigin"`
	}
	if err := json.Unmarshal([]byte(task.InputJSON), &saved); err != nil {
		return err
	}
	if stamp := saved.Stamp; stamp != nil && stamp.ChannelID == channel.ID && stamp.RouteRun == task.RouteRun {
		expected, err := s.videoReceiptOriginMAC(task, channel.ID, current)
		if err != nil {
			return err
		}
		recorded, err := hex.DecodeString(stamp.MAC)
		if err != nil || !hmac.Equal(expected, recorded) {
			return mismatch
		}
		return nil
	}
	// Legacy job without an origin snapshot or retained create log. An
	// unchanged channel row still independently identifies its original URL.
	if !channel.UpdatedAt.IsZero() && !since.IsZero() && !channel.UpdatedAt.After(since) {
		return nil
	}
	return failedPrecondition("缺少可核实的原视频地址且渠道已更新，请核对原回执")
}
