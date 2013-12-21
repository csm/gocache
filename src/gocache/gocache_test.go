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
)

func TestBasic(t *testing.T) {
	spec := CacheSpec{}
	cache := NewManualCache(spec)
	cache.Put("x", "the value for x")
	if _, p := cache.GetIfPresent("x"); !p {
		t.Fail()
	}
	cache.Invalidate("x")
	if _, p := cache.GetIfPresent("x"); p {
		t.Fail()
	}
}

func simpleLoader(key string) interface{} {
	return key
}

func TestLoadingCache(t *testing.T) {
	spec := LoadingCacheSpec{ loader: simpleLoader }
	cache := NewLoadingCache(spec)
	x, p := cache.Get("foo")
	if !p {
		t.Fail()
	}
	if x != "foo" {
		t.Fail()
	}
}
