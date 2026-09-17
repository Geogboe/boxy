package diagnostics

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Seed directly so setup does not benchmark repeated compaction itself.
func BenchmarkFileStoreAppendRetainedHistory(b *testing.B) {
	for _, count := range []int{0, 1000, 10000} {
		b.Run(fmt.Sprintf("events_%d", count), func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "diagnostics.jsonl")
			file, err := os.Create(path)
			if err != nil {
				b.Fatal(err)
			}
			writer := bufio.NewWriter(file)
			encoder := json.NewEncoder(writer)
			stamp := time.Now().UTC()
			for i := 0; i < count; i++ {
				event := Event{ID: fmt.Sprintf("synthetic-%d", i), Timestamp: stamp, Level: "INFO", Message: strings.Repeat("x", 128)}
				if err := encoder.Encode(event); err != nil {
					b.Fatal(err)
				}
			}
			if err := writer.Flush(); err != nil {
				b.Fatal(err)
			}
			if err := file.Close(); err != nil {
				b.Fatal(err)
			}
			store, err := NewFileStore(path, DefaultMaxBytes, DefaultMaxAge)
			if err != nil {
				b.Fatal(err)
			}
			// Include the initial snapshot load in this benchmark.
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := store.Append(context.Background(), Event{ID: fmt.Sprintf("new-%d", i), Level: "INFO", Message: "synthetic append"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkFileStoreAppendWarmWindow(b *testing.B) {
	for _, full := range []bool{false, true} {
		name := "growing"
		if full {
			name = "full"
		}
		b.Run(name, func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "events.jsonl")
			file, err := os.Create(path)
			if err != nil {
				b.Fatal(err)
			}
			writer := bufio.NewWriter(file)
			encoder := json.NewEncoder(writer)
			stamp := time.Now().UTC()
			message := strings.Repeat("x", 128)
			for i := 0; i < 10000; i++ {
				if err := encoder.Encode(Event{ID: fmt.Sprintf("event-%06d", i), Timestamp: stamp, Level: "INFO", Message: message}); err != nil {
					b.Fatal(err)
				}
			}
			if err := writer.Flush(); err != nil {
				b.Fatal(err)
			}
			if err := file.Close(); err != nil {
				b.Fatal(err)
			}
			limit := int64(DefaultMaxBytes)
			if full {
				info, err := os.Stat(path)
				if err != nil {
					b.Fatal(err)
				}
				limit = info.Size()
			}
			store, err := NewFileStore(path, limit, DefaultMaxAge)
			if err != nil {
				b.Fatal(err)
			}
			if err := store.Append(context.Background(), Event{ID: "event-010000", Timestamp: stamp.Add(time.Second), Level: "INFO", Message: message}); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := store.Append(context.Background(), Event{ID: fmt.Sprintf("event-%06d", 10001+i), Timestamp: stamp.Add(time.Duration(i+2) * time.Second), Level: "INFO", Message: message}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
