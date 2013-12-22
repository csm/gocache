/*
 * Copyright 2013 Memeo, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package gocache

import (
	"sync"
	"time"
)

func hashcode(s string) uint32 {
	var code uint32 = 1
	for i := range(s) {
		code += (code * 37) + uint32(s[i])
	}
	return code
}

type Cache interface {
	// Fetch the number of entries in the cache.
	Size() uint

	// Get a value from the cache, if present. If not present,
	// query the supplied value loader for the value. If the
	// value loader returns a value, it is placed into the cache
	// and is returned.
	Get(key string, loader ValueLoader) (interface{}, bool)

	// Get a value from the cache, if present. If not, do not
	// query any value loader.
	GetIfPresent(key string) (interface{}, bool)

	// Put a value into the cache, possibly replacing an existing
	// value.
	Put(key string, value interface{}) bool

	// Remove a key from the cache.
	Invalidate(key string)

	// Perform any cleanup of the cache, such as trimming the
	// cache to the maximum size, or expiring old entries.
	// Note that this is called during other operations (except Size
	// queries) but is not called automatically at any other time.
	// Thus, if you need to
	Cleanup()
}

// A base cache spec.
type CacheSpec struct {
	// The maximum size of the cache. If zero, the cache is unbounded.
	maxSize uint

	// A duration after which to expire entries when written to the
	// cache. If zero, entries are not expired based on write time.
	expireAfterWrite time.Duration

	// A duration after which to expire entries when read from the
	// cache. If zero, entries are not expired based on access time.
	expireAfterAccess time.Duration

	// The concurrency level. This will control the number of shards
	// in the cache, which will affect how many concurrent threads
	// can access the cache at once.
	concurrencyLevel uint

	// A callback function that is called when entries are removed
	// from the cache.
	removalListener RemovalListener
}

type ValueLoader func(key string) interface{}

// A reason why a cache entry was removed.
type RemovalReason int

const (
	// The entry was removed because it expired, either by
	// write time, or read time.
	Expired RemovalReason = iota

	// The entry was explicitly removed with Invalidate.
	Explicit

	// The entry was overwritten with a new value.
	Replaced

	// The entry was removed because the cache was too large.
	Size
)

type RemovalListener func(key string, value interface{}, reason RemovalReason)

// A loading cache spec.
type LoadingCacheSpec struct {
	CacheSpec
	loader ValueLoader
}

type entry struct {
	value interface{}
	putTime time.Time
	getTime time.Time
}

type shard struct {
	values map[string]*entry
	lock sync.RWMutex
}

type baseCache struct {
	Cache
	spec CacheSpec
	shards []shard
	removeShard uint
}

type ManualCache struct {
	baseCache
}

type LoadingCache struct {
	baseCache
	spec LoadingCacheSpec
}

func NewManualCache(spec CacheSpec) *ManualCache {
	var result *ManualCache = new(ManualCache)
	result.spec = spec
	if spec.concurrencyLevel < 1 {
		spec.concurrencyLevel = 1
	}
	result.shards = make([]shard, spec.concurrencyLevel)
	for i := range result.shards {
		result.shards[i] = shard { values: map[string]*entry{}, lock: sync.RWMutex{} }
	}
	result.removeShard = 0
	return result
}

func NewLoadingCache(spec LoadingCacheSpec) *LoadingCache {
	var result *LoadingCache = new(LoadingCache)
	result.spec = spec
	if spec.concurrencyLevel < 1 {
		spec.concurrencyLevel = 1
	}
	result.shards = make([]shard, spec.concurrencyLevel)
	for i := range result.shards {
		result.shards[i] = shard { values: map[string]*entry{}, lock: sync.RWMutex{} }
	}
	result.removeShard = 0
	return result
}

func (self *baseCache) Size() uint {
	var count uint = 0
	for i := range self.shards {
		count += uint(len(self.shards[i].values))
	}
	return count
}

func (self *baseCache) getShard(key string) shard {
	return self.shards[hashcode(key) % uint32(len(self.shards))]
}

func (self *baseCache) GetIfPresent(key string) (interface{}, bool) {
	s := self.getShard(key)
	s.lock.RLock()
	e, present := s.values[key]
	if present {
		e.getTime = time.Now()
	}
	s.lock.RUnlock()
	go self.Cleanup()
	if present {
		return e.value, present
	}
	return nil, false
}

func (self *baseCache) Get(key string, loader ValueLoader) (interface{}, bool) {
	value, present := self.GetIfPresent(key)
	if !present && loader != nil {
		value = loader(key)
		if value != nil {
			s := self.getShard(key)
			s.lock.Lock()
			e := new(entry)
			e.value = value
			e.putTime = time.Now()
			e.getTime = time.Now()
			s.values[key] = e
			s.lock.Unlock()
			return value, true
		}
	}
	return value, present
}

func (self *LoadingCache) Get(key string) (interface{}, bool) {
	return self.baseCache.Get(key, self.spec.loader)
}

func (self *baseCache) Put(key string, value interface{}) bool {
	var updated = false
	if value != nil {
		s := self.getShard(key)
		s.lock.Lock()
		if x, p := s.values[key]; p {
			if self.spec.removalListener != nil {
				self.spec.removalListener(key, x.value, Replaced)
			}
			updated = true
		}
		e := new(entry)
		e.value = value
		e.putTime = time.Now()
		e.getTime = time.Time{}
		s.values[key] = e
		s.lock.Unlock()
	}
	go self.Cleanup()
	return updated
}

func (self *baseCache) Invalidate(key string) {
	s := self.getShard(key)
	s.lock.Lock()
	if x, p := s.values[key]; p {
		if self.spec.removalListener != nil {
			self.spec.removalListener(key, x.value, Explicit)
		}
		delete(s.values, key)
	}
	s.lock.Unlock()
	go self.Cleanup()
}

func (self *baseCache) Cleanup() {
	// If we have a maximum size, check that the cache is within
	// that size.
	if self.spec.maxSize > 0 && self.Size() > self.spec.maxSize {
		toRemove := self.Size() - self.spec.maxSize
		for i := toRemove; i > 0; {
			var oldest time.Time = time.Now()
			var key string = ""
			var value interface{} = nil
			var shard = self.shards[self.removeShard]
			self.removeShard = (self.removeShard + 1) % uint(len(self.shards))
			shard.lock.RLock()
			for k, v := range shard.values {
				if v.getTime.Before(oldest) {
					oldest = v.getTime
					key = k
					value = v.value
				}
			}
			shard.lock.RUnlock()
			if value != nil {
				shard.lock.Lock()
				delete(shard.values, key)
				if self.spec.removalListener != nil {
					self.spec.removalListener(key, value, Size)
				}
				i--
				shard.lock.Unlock()
			}
		}
	}
	if self.spec.expireAfterAccess > 0 || self.spec.expireAfterWrite > 0 {
		for _, shard := range self.shards {
			var toRemove = map[string]bool {}
			shard.lock.RLock()
			for key, value := range shard.values {
				if self.spec.expireAfterWrite > 0 {
					if value.putTime.Add(self.spec.expireAfterWrite).Before(time.Now()) {
						toRemove[key] = true
					}
				}
				if self.spec.expireAfterAccess > 0 && !value.getTime.IsZero() {
					if value.getTime.Add(self.spec.expireAfterAccess).Before(time.Now()) {
						toRemove[key] = true
					}
				}
			}
			shard.lock.RUnlock()
			shard.lock.Lock()
			for key := range toRemove {
				if x, p := shard.values[key]; p {
					delete(shard.values, key)
					if self.spec.removalListener != nil {
						self.spec.removalListener(key, x.value, Expired)
					}
				}
			}
			shard.lock.Unlock()
		}
	}
}
