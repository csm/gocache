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

	// Get a value from the cache, if present. If this cache is
	// a loading cache, then the value loader function will be
	// queried.
	Get(key string) (interface{}, bool)

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

type RemovalReason int
const (
	Expired RemovalReason = iota
	Explicit
	Replaced
	Size
)

type RemovalListener func(key string, value interface{}, reason RemovalReason)

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
	values map[string]entry
	lock sync.RWMutex
}

type baseCache struct {
	Cache
	shards []shard
}

type ManualCache struct {
	baseCache
	spec CacheSpec
}

type LoadingCache struct {
	baseCache
	spec LoadingCacheSpec
}

func NewLoadingCache(spec LoadingCacheSpec) *LoadingCache {
	var result *LoadingCache = new(LoadingCache)
	result.spec = spec
	if spec.concurrencyLevel < 1 {
		spec.concurrencyLevel = 1
	}
	result.shards = make([]shard, spec.concurrencyLevel)
	for i := range result.shards {
		result.shards[i] = shard { values: map[string]entry{}, lock: sync.RWMutex{} }
	}
	return result
}

func (self *LoadingCache) Size() uint {
	var count uint = 0
	for i := range self.shards {
		count += uint(len(self.shards[i].values))
	}
	return count
}

func (self *LoadingCache) getShard(key string) shard {
	return self.shards[hashcode(key) % uint32(len(self.shards))]
}

func (self *LoadingCache) Get(key string) interface{} {
	s := self.getShard(key)
	s.lock.RLock()
	if entry, present := s.values[key]; present {
		entry.getTime = time.Now()
		s.lock.RUnlock()
		self.Cleanup()
		return entry.value
	}
	s.lock.RUnlock()
	if self.spec.loader != nil {
		value := self.spec.loader(key)
		if value != nil {
			s.lock.Lock()
			s.values[key] = entry {value: value, putTime: time.Now(), getTime: time.Now()}
			s.lock.Unlock()
			return value
		}
	}
	return nil
}

func (self *LoadingCache) Put(key string, value interface{}) bool {
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
		s.values[key] = entry{value: value, putTime: time.Now()}
		s.lock.Unlock()
	}
	self.Cleanup()
	return updated
}

func (self *LoadingCache) Invalidate(key string) {
	s := self.getShard(key)
	s.lock.Lock()
	if x, p := s.values[key]; p {
		if self.spec.removalListener != nil {
			self.spec.removalListener(key, x.value, Explicit)
		}
		delete(s.values, key)
	}
	s.lock.Unlock()
	self.Cleanup()
}

func (self *LoadingCache) Cleanup() {
	// If we have a maximum size, check that the cache is within
	// that size.
	if self.spec.maxSize > 0 && self.Size() > self.spec.maxSize {
		toRemove := self.Size() - self.spec.maxSize
		for i := toRemove; i >= 0; {
			var oldest time.Time = time.Now()
			var key string = ""
			var value interface{} = nil;
			var shard = self.shards[i % uint(len(self.shards))]
			shard.lock.Lock()
			for k, v := range shard.values {
				if v.getTime.Before(oldest) {
					oldest = v.getTime
					key = k
					value = v
				}
			}
			if value != nil {
				delete(shard.values, key)
				if self.spec.removalListener != nil {
					self.spec.removalListener(key, value, Size)
				}
				i--
			}
			shard.lock.Unlock()
		}
	}
	if self.spec.expireAfterAccess > 0 || self.spec.expireAfterWrite > 0 {
		for _, shard := range self.shards {
			var toRemove = map[string]bool {}
			shard.lock.RLock()
			for key, value := range shard.values {
				if self.spec.expireAfterWrite > 0 {
					if value.putTime.Before(time.Now().Add(self.spec.expireAfterWrite)) {
						toRemove[key] = true
					}
				}
				if self.spec.expireAfterAccess > 0 && value.getTime.Unix() > 0 {
					if value.getTime.Before(time.Now().Add(self.spec.expireAfterAccess)) {
						toRemove[key] = true
					}
				}
			}
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
			shard.lock.RUnlock()
		}
	}
}
