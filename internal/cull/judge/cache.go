package judge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/schuettc/tackle/internal/cull/jev"
	tools "github.com/schuettc/tools-common"
)

// Cache stores raw Jev responses, one file per key.
type Cache struct{ Dir string }

// CacheKey keys an answer by what was sent and asked, not by policy, so a
// threshold-only rubric change re-scores from cache.
func CacheKey(stateHash, model, questionsHash string) string {
	return stateHash + "|" + model + "|" + questionsHash
}

func (c Cache) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.Dir, hex.EncodeToString(sum[:])+".json")
}

// Get returns the cached response for key, if any.
func (c Cache) Get(key string) (jev.Response, bool) {
	b, err := os.ReadFile(c.path(key))
	if err != nil {
		return jev.Response{}, false
	}
	var r jev.Response
	if json.Unmarshal(b, &r) != nil {
		return jev.Response{}, false
	}
	return r, true
}

// Put stores a response atomically.
func (c Cache) Put(key string, r jev.Response) error {
	if err := tools.EnsureDir(c.Dir); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return tools.WriteFileAtomic(c.path(key), b, 0o600)
}
