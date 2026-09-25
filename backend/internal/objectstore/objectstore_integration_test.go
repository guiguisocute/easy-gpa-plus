package objectstore

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

func TestGarageRoundTrip(t *testing.T) {
	if os.Getenv("EASYGPA_INTEGRATION") != "1" {
		t.Skip("set EASYGPA_INTEGRATION=1 to run against the development object store")
	}
	client, err := New(Config{
		Endpoint:  os.Getenv("S3_ENDPOINT"),
		Region:    os.Getenv("S3_REGION"),
		AccessKey: os.Getenv("S3_ACCESS_KEY"),
		SecretKey: os.Getenv("S3_SECRET_KEY"),
		Bucket:    os.Getenv("S3_BUCKET"),
		UseSSL:    os.Getenv("S3_USE_SSL") == "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	key := "integration/round-trip.txt"
	payload := []byte("easygpa object-store round trip")
	if err := client.Put(ctx, key, "text/plain", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("put: %v", err)
	}
	defer func() {
		if err := client.Remove(context.Background(), key); err != nil {
			t.Errorf("remove: %v", err)
		}
	}()
	info, err := client.Stat(ctx, key)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", info.Size, len(payload))
	}
}
