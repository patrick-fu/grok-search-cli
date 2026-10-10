package grok

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func requestHeaders(model string) map[string]string {
	session := randomUUID(7)
	namespace, _ := hex.DecodeString("6ba7b8129dad11d180b400c04fd430c8")
	hash := sha1.New()
	hash.Write(namespace)
	hash.Write([]byte("xai:grok-build:conversation-group:" + session))
	group := hash.Sum(nil)[:16]
	group[6] = group[6]&15 | 5<<4
	group[8] = group[8]&63 | 128
	headers := map[string]string{
		"x-grok-conv-id":        session,
		"x-grok-session-id":     session,
		"x-grok-req-id":         randomUUID(4),
		"x-grok-conv-group-id":  formatUUID(group),
		"x-grok-model-override": model,
		"x-grok-turn-idx":       "1",
	}
	if agentID := existingAgentID(); agentID != "" {
		headers["x-grok-agent-id"] = agentID
	}
	return headers
}

func randomUUID(version byte) string {
	var b [16]byte
	rand.Read(b[:])
	if version == 7 {
		millis := uint64(time.Now().UnixMilli())
		for i := 5; i >= 0; i-- {
			b[i] = byte(millis)
			millis >>= 8
		}
	}
	b[6] = b[6]&15 | version<<4
	b[8] = b[8]&63 | 128
	return formatUUID(b[:])
}

func formatUUID(b []byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func existingAgentID() string {
	home := os.Getenv("GROK_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".grok")
	}
	path := filepath.Join(home, "agent_id")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(file, 65))
	if err != nil || len(data) > 64 {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return ""
	}
	b, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(b) != 16 {
		return ""
	}
	return id
}
