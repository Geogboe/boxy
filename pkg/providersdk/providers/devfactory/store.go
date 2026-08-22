package devfactory

import (
	"path/filepath"
	"time"

	"github.com/Geogboe/boxy/pkg/diskjson"
)

const storeFilename = "devfactory.json"

// resourceRecord is the JSON-serializable form of a devfactory resource.
type resourceRecord struct {
	ID             string            `json:"id"`
	State          string            `json:"state"`
	Labels         map[string]string `json:"labels,omitempty"`
	ConnectionInfo map[string]string `json:"connection_info"`
	CreatedAt      time.Time         `json:"created_at"`
	Updates        []string          `json:"updates,omitempty"`
}

// storeData is the top-level structure persisted to devfactory.json.
type storeData struct {
	Resources map[string]*resourceRecord `json:"resources"`
	NextPort  int                        `json:"next_port"`
}

func newStoreData() storeData {
	return storeData{
		Resources: make(map[string]*resourceRecord),
		NextPort:  10000,
	}
}

// normalizeStoreData fills in defaults a decoded storeData might be missing
// — either because the file doesn't exist yet (diskjson.Store's newFunc
// covers that case already, but re-normalizing here is cheap and covers a
// store file written before a field existed) or because a zero-value
// storeData was unmarshaled from an empty/partial file.
func normalizeStoreData(s storeData) storeData {
	if s.Resources == nil {
		s.Resources = make(map[string]*resourceRecord)
	}
	if s.NextPort == 0 {
		s.NextPort = 10000
	}
	return s
}

// newDevfactoryStore builds the diskjson.Store devfactory.json is persisted
// through — one atomically-written JSON blob, mutex-guarded by the Store
// itself. See pkg/diskjson's package doc for why devfactory uses this
// instead of hand-rolling its own load/save (it used to; see #181's design
// spec, "Persistence backend and DataDir resolution").
func newDevfactoryStore(dataDir string) *diskjson.Store[storeData] {
	return diskjson.New(filepath.Join(dataDir, storeFilename), newStoreData)
}
