package broker

import "sync"

// waiterMap is a tiny typed wrapper for sync.Map keyed by [16]byte.
type waiterMap struct {
	sync.Map
}

func (wm *waiterMap) NewWaiter() ([16]byte, chan []byte) {
	id := NewMessageID()
	ch := make(chan []byte, 1)
	wm.Map.Store(id, ch)
	return id, ch
}

func (wm *waiterMap) Delete(id [16]byte) {
	wm.Map.Delete(id)
}

func (wm *waiterMap) LoadAndDelete(id [16]byte) (chan []byte, bool) {
	v, ok := wm.Map.LoadAndDelete(id)
	if !ok {
		return nil, false
	}

	return v.(chan []byte), true
}
