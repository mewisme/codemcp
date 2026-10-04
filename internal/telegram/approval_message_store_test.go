package telegram

import (
	"fmt"
	"os"
	"testing"
)

func TestApprovalMessageStoreBoundsPersistedReferences(t *testing.T) {
	root := t.TempDir()
	store := newApprovalMessageStore(root)
	for index := 0; index < maxApprovalMessageRefs+20; index++ {
		if err := store.put(42, fmt.Sprintf("req_%04d", index), int64(index+1)); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.RLock()
	count := 0
	for _, messages := range store.refs {
		count += len(messages)
	}
	store.mu.RUnlock()
	if count != maxApprovalMessageRefs {
		t.Fatalf("approval refs=%d want=%d", count, maxApprovalMessageRefs)
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxApprovalMessageStoreSize {
		t.Fatalf("approval store size=%d exceeds max=%d", info.Size(), maxApprovalMessageStoreSize)
	}
	reloaded := newApprovalMessageStore(root)
	reloaded.mu.RLock()
	reloadedCount := 0
	for _, messages := range reloaded.refs {
		reloadedCount += len(messages)
	}
	reloaded.mu.RUnlock()
	if reloadedCount != maxApprovalMessageRefs {
		t.Fatalf("reloaded approval refs=%d want=%d", reloadedCount, maxApprovalMessageRefs)
	}
}
