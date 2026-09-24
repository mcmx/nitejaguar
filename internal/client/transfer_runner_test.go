package client

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestTransferTargetFromArgs(t *testing.T) {
	got := transferTargetFromArgs("transfer", map[string]string{
		"destination_client": "client_123", "destination_client_tags": "gpu, fast",
	})
	if got.destClient != "client_123" || len(got.destTags) != 2 || got.destTags[0] != "gpu" {
		t.Fatalf("unexpected target: %+v", got)
	}
	if got := transferTargetFromArgs("file", map[string]string{"destination_client": "x"}); got.destClient != "" {
		t.Fatalf("non-transfer nodes must not route: %+v", got)
	}
	if got := transferTargetFromArgs("transfer", nil); got.destClient != "" || len(got.destTags) != 0 {
		t.Fatalf("empty args must not route: %+v", got)
	}
}

func TestIsRemoteTransfer(t *testing.T) {
	if !isRemoteTransfer(transferTarget{destClient: "client_b"}, "client_a") {
		t.Fatal("other client should be remote")
	}
	if isRemoteTransfer(transferTarget{destClient: "client_a"}, "client_a") {
		t.Fatal("self should be local")
	}
	if !isRemoteTransfer(transferTarget{destTags: []string{"gpu"}}, "client_a") {
		t.Fatal("tag targets resolve server-side to others")
	}
	if isRemoteTransfer(transferTarget{}, "client_a") {
		t.Fatal("no destination should be local")
	}
}

func TestWriteReceivedBytes(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "sub", "out.bin")
	content := []byte("p2p bytes")
	sess := TransferSession{TransferID: "transfer_test", DestinationFile: dst, SHA256: shaOf(content), Size: int64(len(content))}
	if err := writeReceivedBytes(sess, content); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != string(content) {
		t.Fatalf("content = %q, err = %v", got, err)
	}
	// Collision is terminal: existing destination refused.
	if err := writeReceivedBytes(sess, content); err == nil {
		t.Fatal("expected collision error")
	}
	// Tampered bytes rejected.
	sess.DestinationFile = filepath.Join(dir, "other.bin")
	if err := writeReceivedBytes(sess, []byte("tampered!!")); err == nil {
		t.Fatal("expected sha mismatch error")
	}
}

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
