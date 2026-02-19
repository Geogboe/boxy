package devboxes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const storeFilename = "devboxes.json"

// resourceRecord is the JSON-serializable form of a devbox resource.
type resourceRecord struct {
	ID             string            `json:"id"`
	State          string            `json:"state"`
	Labels         map[string]string `json:"labels,omitempty"`
	ConnectionInfo map[string]string `json:"connection_info"`
	CreatedAt      time.Time         `json:"created_at"`
	Updates        []string          `json:"updates,omitempty"`
}

// storeData is the top-level structure persisted to devboxes.json.
type storeData struct {
	Resources map[string]*resourceRecord `json:"resources"`
	NextPort  int                        `json:"next_port"`
}

func newStoreData() *storeData {
	return &storeData{
		Resources: make(map[string]*resourceRecord),
		NextPort:  10000,
	}
}

// loadStore reads the store file from disk. Returns empty store if the
// file doesn't exist yet.
func loadStore(dataDir string) (*storeData, error) {
	path := filepath.Join(dataDir, storeFilename)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newStoreData(), nil
		}
		return nil, err
	}

	var s storeData
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Resources == nil {
		s.Resources = make(map[string]*resourceRecord)
	}
	if s.NextPort == 0 {
		s.NextPort = 10000
	}
	return &s, nil
}

// saveStore writes the store to disk as indented JSON.
func saveStore(dataDir string, s *storeData) error {
	path := filepath.Join(dataDir, storeFilename)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
