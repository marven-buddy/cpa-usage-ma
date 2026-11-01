package main

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validNoteKey(key string) bool {
	if key == "client:unknown" || key == "legacy:unknown" {
		return true
	}
	for _, prefix := range []string{"client-", "legacy-"} {
		if digest, ok := strings.CutPrefix(key, prefix); ok && len(digest) == 64 {
			_, err := hex.DecodeString(digest)
			return err == nil && digest == strings.ToLower(digest)
		}
	}
	return false
}

func setKeyNote(body []byte) error {
	var request struct {
		Key  string `json:"key"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("无效的备注请求")
	}
	if !validNoteKey(request.Key) {
		return fmt.Errorf("无效的 Key 标识")
	}
	request.Note = strings.TrimSpace(request.Note)
	if utf8.RuneCountInString(request.Note) > 80 || strings.ContainsFunc(request.Note, unicode.IsControl) {
		return fmt.Errorf("备注最多 80 个字符，且不能包含换行或控制字符")
	}
	ensureDB()
	mu.Lock()
	defer mu.Unlock()
	if db == nil {
		return fmt.Errorf("用量数据库不可用")
	}
	if request.Note == "" {
		_, err := db.Exec("DELETE FROM key_notes WHERE key_id=?", request.Key)
		return err
	}
	_, err := db.Exec("INSERT INTO key_notes (key_id,note) VALUES (?,?) ON CONFLICT(key_id) DO UPDATE SET note=excluded.note", request.Key, request.Note)
	return err
}

func loadKeyNotes(conn *sql.DB) (map[string]string, error) {
	rows, err := conn.Query("SELECT key_id,note FROM key_notes")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := map[string]string{}
	for rows.Next() {
		var key, note string
		if err := rows.Scan(&key, &note); err != nil {
			return nil, err
		}
		notes[key] = note
	}
	return notes, rows.Err()
}
