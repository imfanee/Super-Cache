// Store micro-benchmarks for GET/SET throughput (P5.5).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package store

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/supercache/supercache/internal/config"
)

func BenchmarkSetGet(b *testing.B) {
	cfg := &config.Config{SharedSecret: strings.Repeat("s", 32)}
	config.ApplyDefaults(cfg)
	s, err := NewStore(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	key := []byte("bench_key")
	val := []byte("bench_value")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.Set(string(key), val, time.Time{}); err != nil {
			b.Fatal(err)
		}
		if _, err := s.Get(string(key)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStoreGet_Parallel(b *testing.B) {
	cfg := &config.Config{SharedSecret: strings.Repeat("s", 32)}
	config.ApplyDefaults(cfg)
	s, err := NewStore(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			k := string(rune('a'+(i%26))) + string(rune('A'+((i/26)%26)))
			_ = s.Set(k, []byte("v"), time.Time{})
			_, _ = s.Get(k)
		}
	})
}

func BenchmarkStoreSet_Parallel(b *testing.B) {
	cfg := &config.Config{SharedSecret: strings.Repeat("s", 32)}
	config.ApplyDefaults(cfg)
	s, err := NewStore(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			i++
			k := string(rune('a'+(i%26))) + string(rune('A'+((i/26)%26)))
			_ = s.Set(k, []byte("v"), time.Time{})
		}
	})
}

func BenchmarkStoreShard_Contention(b *testing.B) {
	cfg := &config.Config{SharedSecret: strings.Repeat("s", 32)}
	config.ApplyDefaults(cfg)
	s, err := NewStore(cfg)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	b.ReportAllocs()
	b.SetParallelism(runtime.NumCPU())

	b.Run("distinct_keys", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			g := 0
			for pb.Next() {
				g++
				k := fmt.Sprintf("k%08d", g)
				_ = s.Set(k, []byte("x"), time.Time{})
				_, _ = s.Get(k)
			}
		})
	})

	b.Run("shared_256", func(b *testing.B) {
		for i := 0; i < 256; i++ {
			_ = s.Set(fmt.Sprintf("c%d", i), []byte("y"), time.Time{})
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			g := 0
			for pb.Next() {
				g++
				k := fmt.Sprintf("c%d", g%256)
				_ = s.Set(k, []byte("x"), time.Time{})
				_, _ = s.Get(k)
			}
		})
	})
}
