package kit

import (
	"sync"
)

type Filter func(v interface{}) bool

type OrderedMap interface {
	Count() int
	Put(k, v interface{})
	Get(k interface{}) interface{}
	Index(i int) interface{}
	Remove(k interface{})
	RemoveAll(fn Filter)
	ForEach(fn Filter)
}

func NewOrderedMap() *omap {
	return &omap{m: make(map[interface{}]interface{}), keys: []interface{}{}}
}

type omap struct {
	m    map[interface{}]interface{}
	keys []interface{}
	mu   sync.Mutex
}

func (om *omap) Put(k, v interface{}) {
	om.mu.Lock()
	defer om.mu.Unlock()

	if _, ok := om.m[k]; ok {
		om.m[k] = v
		return
	} else {
		om.m[k] = v
	}
	om.keys = append(om.keys, k)
}

func (om *omap) Get(k interface{}) interface{} {
	om.mu.Lock()
	defer om.mu.Unlock()
	
	return om.m[k]
}

func (om *omap) Index(i int) interface{} {
	om.mu.Lock()
	defer om.mu.Unlock()
	return om.m[om.keys[i]]
}

func (om *omap) Count() int {
	return len(om.keys)
}

func (om *omap) Remove(k interface{}) {
	om.mu.Lock()
	defer om.mu.Unlock()

	delete(om.m, k)
	n := len(om.keys)
	i := 0
	for i = range om.keys {
		if k == om.keys[i] {
			break
		}
	}

	if i >= n {
		return
	}
	om.keys[i] = om.keys[n-1]
	om.keys = om.keys[:n-1]
}

func (om *omap) RemoveAll(fn Filter) {	 
	om.mu.Lock()
	defer om.mu.Unlock()

	for k, v := range om.m {
		if fn != nil {
			fn(v)
		}
		delete(om.m, k)
	}
	om.keys = om.keys[:0]
}

func (om *omap) ForEach(fn Filter) {
	om.mu.Lock()
	defer om.mu.Unlock()

	for _, k := range om.keys {
		if fn != nil && !fn(om.m[k]) {
			return
		}
	}
}
