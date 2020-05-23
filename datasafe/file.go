// SPDX-License-Identifier: Apache-2.0
// Copyright 2020 Marcus Soll
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package datasafe

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/registry"
)

func init() {
	f := new(file)
	f.mutex = new(sync.Mutex)
	f.path = "./data/"

	err := registry.RegisterDataSafe(f, "file")
	if err != nil {
		panic(err)
	}
}

type file struct {
	path  string
	mutex *sync.Mutex
}

func (f *file) InitialiseDatasafe(config []byte) error {
	counter.StartProcess()
	defer counter.EndProcess()
	f.mutex.Lock()
	defer f.mutex.Unlock()

	err := os.MkdirAll(filepath.Join(f.path, "config"), os.ModePerm)
	if err != nil {
		return err
	}
	err = os.MkdirAll(filepath.Join(f.path, "announcements"), os.ModePerm)
	if err != nil {
		return err
	}
	return nil
}

func (f *file) GetConfig(key, plugin string) ([]byte, error) {
	counter.StartProcess()
	defer counter.EndProcess()
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if strings.Contains(key, "﷐") || strings.Contains(plugin, "﷐") {
		return nil, errors.New("Unallowed characters found")
	}
	key = strings.ReplaceAll(key, string(os.PathSeparator), "﷐")
	plugin = strings.ReplaceAll(plugin, string(os.PathSeparator), "﷐")

	b, err := ioutil.ReadFile(filepath.Join(f.path, "config", key, plugin))

	if os.IsNotExist(err) {
		// No error - no configuration was saved
		return nil, nil
	}

	return b, err
}

func (f *file) SetConfig(key, plugin string, config []byte) error {
	counter.StartProcess()
	defer counter.EndProcess()
	f.mutex.Lock()
	defer f.mutex.Unlock()

	if strings.Contains(key, "﷐") || strings.Contains(plugin, "﷐") {
		return errors.New("Unallowed characters found")
	}
	key = strings.ReplaceAll(key, string(os.PathSeparator), "﷐")
	plugin = strings.ReplaceAll(plugin, string(os.PathSeparator), "﷐")

	err := os.MkdirAll(filepath.Join(f.path, "config", key), os.ModePerm)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filepath.Join(f.path, "config", key, plugin), config, os.ModePerm)
}

func (f *file) SaveAnnouncement(key string, announcement registry.Announcement) (string, error) {
	counter.StartProcess()
	defer counter.EndProcess()
	f.mutex.Lock()
	defer f.mutex.Unlock()

	a, err := f.internalLoad(key)
	if err != nil {
		return "", err
	}
	a = append(a, announcement)
	id := strconv.Itoa(len(a))
	err = f.internalSave(key, a)
	return id, err
}

func (f *file) GetAnnouncement(key, id string) (registry.Announcement, error) {
	counter.StartProcess()
	defer counter.EndProcess()

	i, err := strconv.Atoi(id)
	if err != nil {
		return registry.Announcement{}, err
	}

	f.mutex.Lock()
	defer f.mutex.Unlock()

	a, err := f.internalLoad(key)
	if len(a) == 0 {
		return registry.Announcement{}, errors.New("no announcements")
	}
	if err != nil {
		return registry.Announcement{}, err
	}
	if i > len(a) || i <= 0 {
		return registry.Announcement{}, fmt.Errorf("unknown id %s", id)
	}
	return a[i-1], nil
}

func (f *file) GetAllAnnouncements(key string) ([]registry.Announcement, error) {
	counter.StartProcess()
	defer counter.EndProcess()
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.internalLoad(key)
}

func (f *file) GetAnnouncementKeys(key string) ([]string, error) {
	counter.StartProcess()
	defer counter.EndProcess()
	f.mutex.Lock()
	defer f.mutex.Unlock()

	a, err := f.internalLoad(key)
	if err != nil {
		return nil, err
	}
	s := make([]string, len(a))
	for i := 0; i < len(s); i++ {
		s[i] = strconv.Itoa(i + 1)
	}
	return s, nil
}

func (f *file) internalLoad(key string) ([]registry.Announcement, error) {
	// f must be locked by caller
	if strings.Contains(key, "﷐") {
		return nil, errors.New("Unallowed characters found")
	}
	key = strings.ReplaceAll(key, string(os.PathSeparator), "﷐")

	var a []registry.Announcement
	file, err := os.Open(filepath.Join(f.path, "announcements", key))
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	defer file.Close()
	dec := gob.NewDecoder(file)
	err = dec.Decode(&a)
	return a, err
}

func (f *file) internalSave(key string, a []registry.Announcement) error {
	// f must be locked by caller
	counter.StartProcess()
	defer counter.EndProcess()
	if strings.Contains(key, "﷐") {
		return errors.New("Unallowed characters found")
	}
	key = strings.ReplaceAll(key, string(os.PathSeparator), "﷐")

	file, err := os.Create(filepath.Join(f.path, "announcements", key))
	if err != nil {
		return err
	}
	defer file.Close()
	enc := gob.NewEncoder(file)
	err = enc.Encode(&a)
	return err
}
