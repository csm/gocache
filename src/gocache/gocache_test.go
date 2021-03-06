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
	"testing"
	"time"
	"fmt"
)

func TestBasic(t *testing.T) {
	spec := CacheSpec{}
	cache := NewManualCache(spec)
	cache.Put("x", "the value for x")
	if cache.Size() != 1 {
		t.Fail()
	}
	if _, p := cache.GetIfPresent("x"); !p {
		t.Fail()
	}
	cache.Invalidate("x")
	if cache.Size() != 0 {
		t.Fail()
	}
	if _, p := cache.GetIfPresent("x"); p {
		t.Fail()
	}
}

func simpleLoader(key string) (interface{}, error) {
	return key, nil
}

func TestLoadingCache(t *testing.T) {
	spec := LoadingCacheSpec{ Loader: simpleLoader }
	cache := NewLoadingCache(spec)
	x, p, err := cache.Get("foo")
	if !p || err != nil {
		t.Fail()
	}
	if x != "foo" {
		t.Fail()
	}
}

func TestWriteExpiration(t *testing.T) {
	spec := CacheSpec{ExpireAfterWrite:time.Second / 10,
		RemovalListener:func(k string, v interface{}, code RemovalReason) {
			if code != Expired {
				t.Fail()
			}
		}}
	cache := NewManualCache(spec)
	cache.Put("foo", "bar")
	time.Sleep(time.Second / 10)
	cache.Cleanup()
	if _, p := cache.GetIfPresent("foo"); p {
		t.Fail()
	}
}

func TestAccessExpiration(t *testing.T) {
	spec := CacheSpec{ExpireAfterAccess:time.Second / 10,
		RemovalListener:func(k string, v interface{}, code RemovalReason) {
			if code != Expired {
				t.Fail()
			}
		}}
	cache := NewManualCache(spec)
	cache.Put("foo", "bar")
	if _, p := cache.GetIfPresent("foo"); !p {
		t.Log("accessing foo, should be there")
		t.Fail()
	}
	time.Sleep(time.Second / 10)
	cache.Cleanup()
	if _, p := cache.GetIfPresent("foo"); p {
		t.Log("accessing foo, should not be there")
		t.Fail()
	}
}

func TestMaxSize(t *testing.T) {
	spec := CacheSpec{MaxSize:10,
		RemovalListener:func(k string, v interface{}, code RemovalReason) {
			if code != Size {
				t.Fail()
			}
		}}
	cache := NewManualCache(spec)
	for i := 0; i < 20; i++ {
		cache.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
	}
	cache.Cleanup()
	if cache.Size() > 10 {
		t.Fail()
	}
}

func TestMaxSize2(t *testing.T) {
	spec := CacheSpec{MaxSize:10,
		ConcurrencyLevel: 8,
		RemovalListener:func(k string, v interface{}, code RemovalReason) {
			if code != Size {
				t.Fail()
			}
		}}
	cache := NewManualCache(spec)
	for i := 0; i < 20; i++ {
		cache.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
	}
	cache.Cleanup()
	if cache.Size() > 10 {
		t.Fail()
	}
}
