package weblog

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/redis/go-redis/v9"

	"dash_go/internal/core/pyobj"
)

// redisLiveLog reads the newest entries from the Python Dash Redis list ("web_log").
// Each list element is stored by Python rd_rpush() as zlib.compress(pickle.dumps(str)), decoded safely via pyobj.Decode.
type redisLiveLog struct {
	rdb *redis.Client
	key string
}

// NewRedisLiveLog returns a LiveLogStore reading listKey (default "web_log") from rdb.
func NewRedisLiveLog(rdb *redis.Client, listKey string) LiveLogStore {
	if rdb == nil {
		return nil
	}
	if strings.TrimSpace(listKey) == "" {
		listKey = "web_log"
	}
	return &redisLiveLog{rdb: rdb, key: listKey}
}

func (s *redisLiveLog) Recent(ctx context.Context, limit int) ([]LiveLogEntry, int64, error) {
	if limit <= 0 {
		limit = DefaultLiveLimit
	}
	if limit > MaxLiveLimit {
		limit = MaxLiveLimit
	}
	pipe := s.rdb.Pipeline()
	lenCmd := pipe.LLen(ctx, s.key)
	rangeCmd := pipe.LRange(ctx, s.key, int64(-limit), -1)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, 0, err
	}
	total := lenCmd.Val()
	rawList := rangeCmd.Val()

	entries := make([]LiveLogEntry, 0, len(rawList))
	// Reverse so the newest entry appears first, matching legacy IE.py build_web_log().
	for i := len(rawList) - 1; i >= 0; i-- {
		text := decodeRedisLogItem([]byte(rawList[i]))
		entry := ParseLiveLogLine(text)
		entry.Seq = len(entries) + 1
		entries = append(entries, entry)
	}
	return entries, total, nil
}

// decodeRedisLogItem decodes zlib.compress(pickle.dumps(v)) safely via pyobj.Decode, falling back to plain UTF-8
// text just like Python LocalListV3.rd_lrange().
func decodeRedisLogItem(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if val, err := pyobj.Decode(raw); err == nil {
		switch v := val.(type) {
		case string:
			return v
		case []byte:
			return string(v)
		case nil:
			return ""
		default:
			return fmt.Sprint(v)
		}
	}
	return string(raw)
}

var (
	reStatus3 = regexp.MustCompile(`^\d{3}$`)
	reHost    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,63}$`)
	reDate    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}`)
)

var validLogLevels = map[string]bool{
	"debug":    true,
	"info":     true,
	"warning":  true,
	"warn":     true,
	"error":    true,
	"critical": true,
}

// ParseLiveLogLine parses a web_log line written by Python rd_log():
//
//	<ip> | <timestamp> | <app: action or message> | <status> | <level> | <host>
//
// Older lines without host (5 segments) or arbitrary plain lines are still returned cleanly with Raw preserved.
func ParseLiveLogLine(line string) LiveLogEntry {
	trimmed := strings.TrimSpace(line)
	entry := LiveLogEntry{
		Raw:     trimmed,
		Message: trimmed,
		Level:   "info",
	}
	if trimmed == "" {
		return entry
	}
	parts := strings.Split(trimmed, " | ")
	if len(parts) < 4 {
		return entry
	}

	ip := strings.TrimSpace(parts[0])
	ts := strings.TrimSpace(parts[1])
	if !reDate.MatchString(ts) {
		return entry
	}

	tail := parts[2:]
	var host, level, status string

	// Strip optional trailing host, level, status from the right so messages containing " | " stay intact.
	if len(tail) >= 4 {
		candHost := strings.TrimSpace(tail[len(tail)-1])
		candLevel := strings.ToLower(strings.TrimSpace(tail[len(tail)-2]))
		candStatus := strings.TrimSpace(tail[len(tail)-3])
		if reHost.MatchString(candHost) && validLogLevels[candLevel] && reStatus3.MatchString(candStatus) {
			host = candHost
			level = normalizeLevel(candLevel)
			status = candStatus
			tail = tail[:len(tail)-3]
		}
	}
	if status == "" && len(tail) >= 3 {
		candLevel := strings.ToLower(strings.TrimSpace(tail[len(tail)-1]))
		candStatus := strings.TrimSpace(tail[len(tail)-2])
		if validLogLevels[candLevel] && reStatus3.MatchString(candStatus) {
			level = normalizeLevel(candLevel)
			status = candStatus
			tail = tail[:len(tail)-2]
		}
	}
	if status == "" && len(tail) >= 2 {
		candStatus := strings.TrimSpace(tail[len(tail)-1])
		if reStatus3.MatchString(candStatus) {
			status = candStatus
			tail = tail[:len(tail)-1]
		}
	}

	msg := strings.TrimSpace(strings.Join(tail, " | "))
	entry.IP = ip
	entry.Timestamp = ts
	entry.Message = msg
	entry.Status = status
	if level != "" {
		entry.Level = level
	}
	entry.Host = host

	if app, action, ok := strings.Cut(msg, ": "); ok {
		a := strings.TrimSpace(app)
		if a != "" && len(a) <= 32 && !strings.Contains(a, " ") {
			entry.App = a
			entry.Action = strings.TrimSpace(action)
		}
	}
	return entry
}

func normalizeLevel(l string) string {
	switch strings.ToLower(strings.TrimSpace(l)) {
	case "warn", "warning":
		return "warning"
	case "err", "error":
		return "error"
	case "critical", "fatal":
		return "critical"
	case "debug":
		return "debug"
	default:
		return "info"
	}
}
