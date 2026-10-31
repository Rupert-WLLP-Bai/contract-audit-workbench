package store

import (
	"testing"
	"time"
)

func TestReadOnlyGetterDoesNotWaitForBusinessMutexAndSetterDoes(t *testing.T) {
	st := &Store{}
	st.UseReadOnly(true)

	st.mu.Lock()
	locked := true
	defer func() {
		if locked {
			st.mu.Unlock()
		}
	}()

	read := make(chan bool, 1)
	go func() { read <- st.ReadOnly() }()
	select {
	case got := <-read:
		if !got {
			t.Fatal("read-only getter returned the wrong value")
		}
	case <-time.After(time.Second):
		t.Fatal("read-only getter waited for the business mutex")
	}

	started := make(chan struct{})
	set := make(chan struct{})
	go func() {
		close(started)
		st.UseReadOnly(false)
		close(set)
	}()
	<-started
	select {
	case <-set:
		t.Fatal("read-only setter bypassed the business-mutex barrier")
	case <-time.After(20 * time.Millisecond):
	}

	st.mu.Unlock()
	locked = false
	select {
	case <-set:
	case <-time.After(time.Second):
		t.Fatal("read-only setter did not complete after the mutex was released")
	}
	if st.ReadOnly() {
		t.Fatal("read-only setter did not publish the updated value")
	}
}
