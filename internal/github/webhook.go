package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// PushEvent captures the minimal normalized fields from a GitHub push webhook.
type PushEvent struct {
	RepoFullName  string
	Branch        string
	CommitSHA     string
	CommitMessage string
	Deleted       bool
	Pusher        string
}

// VerifySignature validates X-Hub-Signature-256 against the webhook secret in constant time.
func VerifySignature(secret string, payload []byte, signatureHeader string) bool {
	if secret == "" || !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}
	providedHex := strings.TrimPrefix(signatureHeader, "sha256=")
	providedMAC, err := hex.DecodeString(providedHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := mac.Sum(nil)
	return hmac.Equal(providedMAC, expectedMAC)
}

// ParsePushEvent decodes a GitHub push webhook body and extracts branch + commit info.
func ParsePushEvent(payload []byte) (*PushEvent, error) {
	var raw struct {
		Ref        string `json:"ref"`
		After      string `json:"after"`
		Deleted    bool   `json:"deleted"`
		HeadCommit *struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"head_commit"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
		Pusher struct {
			Name string `json:"name"`
		} `json:"pusher"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("decode push payload: %w", err)
	}
	if raw.Repository.FullName == "" {
		return nil, fmt.Errorf("missing repository.full_name")
	}
	branch := strings.TrimPrefix(raw.Ref, "refs/heads/")
	sha := raw.After
	var msg string
	if raw.HeadCommit != nil {
		if sha == "" || strings.HasPrefix(sha, "0000000") {
			sha = raw.HeadCommit.ID
		}
		msg = strings.Split(raw.HeadCommit.Message, "\n")[0]
	}
	return &PushEvent{
		RepoFullName:  raw.Repository.FullName,
		Branch:        branch,
		CommitSHA:     sha,
		CommitMessage: msg,
		Deleted:       raw.Deleted,
		Pusher:        raw.Pusher.Name,
	}, nil
}
