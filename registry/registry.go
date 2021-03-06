// SPDX-License-Identifier: Apache-2.0
// Copyright 2020,2021 Marcus Soll
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

// Package registry provides a central way to register and use all available saving backends and plugins.
// All options should be registered prior to the program starting, normally through init().
package registry

import (
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"
)

// CurrentDataSafe holds the current data safe.
// It must be saved before loading any Plugin and never be changed after that.
var CurrentDataSafe DataSafe = nil

// AlreadyRegisteredError represents an error where an option is already registeres
type AlreadyRegisteredError string

// Error returns the error description
func (a AlreadyRegisteredError) Error() string {
	return string(a)
}

// PluginFactory represents a function to generate a new Plugin.
// The Plugin loads its configuration from the DataSafe.
type PluginFactory func(key, shortDescription string, errorChannel chan string) (Plugin, error)

// Plugin represents an announcement plugin.
// All methods must be save to use in parallel.
type Plugin interface {
	GetConfig() template.HTML
	ProcessConfigChange(r *http.Request) error
	NewAnnouncement(a Announcement, id string)
}

// Announcement represents a single announcement.
// It has two main parts: a short header (something like a short summary) and the actual message.
// Time contains the publication time of the announcement.
type Announcement struct {
	Header, Message string
	Time            time.Time
}

// DataSafe represents a backend for save storage of questionnaire results.
// The keys of the announcement should be kept in the order they arrive.
type DataSafe interface {
	InitialiseDatasafe(config []byte) error
	GetConfig(key, plugin string) ([]byte, error)
	SetConfig(key, plugin string, config []byte) error
	SaveAnnouncement(key string, a Announcement) (id string, err error)
	GetAnnouncement(key, id string) (Announcement, error)
	GetAllAnnouncements(key string) ([]Announcement, error)
	GetAnnouncementKeys(key string) ([]string, error)
}

// PasswordMethod enables to compare the password against different 'truth'.
// The truth might be plain text, a password hash or similar.
// Truth must contain every information needed to compare the password.
// The function must be callable in parallel at the same time.
// The bool represents whether comparison is successful. Error is returned if there is any error during computation.
type PasswordMethod func(password, truth string) (bool, error)

var (
	knownPlugins              = make(map[string]PluginFactory)
	knownPluginsMutex         = sync.RWMutex{}
	knownDataSafes            = make(map[string]DataSafe)
	knownDataSafesMutex       = sync.RWMutex{}
	knownPasswordMethods      = make(map[string]PasswordMethod)
	knownPasswordMethodsMutex = sync.RWMutex{}
)

// RegisterPlugin registeres a plugin.
// The name of the plugin is used as an identifier and must be unique.
// You can savely use it in parallel.
func RegisterPlugin(f PluginFactory, name string) error {
	knownPluginsMutex.Lock()
	defer knownPluginsMutex.Unlock()

	_, ok := knownPlugins[name]
	if ok {
		return AlreadyRegisteredError("Question already registered")
	}
	knownPlugins[name] = f
	return nil
}

// GetPlugin returns a plugin factory.
// The bool indicates whether it existed. You can only use it if the bool is true.
func GetPlugin(name string) (PluginFactory, bool) {
	knownPluginsMutex.RLock()
	defer knownPluginsMutex.RUnlock()
	f, ok := knownPlugins[name]
	return f, ok
}

// RegisterDataSafe registeres a data safe.
// The name of the data safe is used as an identifier and must be unique.
// You can savely use it in parallel.
func RegisterDataSafe(t DataSafe, name string) error {
	knownDataSafesMutex.Lock()
	defer knownDataSafesMutex.Unlock()

	_, ok := knownDataSafes[name]
	if ok {
		return AlreadyRegisteredError("DataSafe already registered")
	}
	knownDataSafes[name] = t
	return nil
}

// GetDataSafe returns a data safe.
// The bool indicates whether it existed. You can only use it if the bool is true.
func GetDataSafe(name string) (DataSafe, bool) {
	knownDataSafesMutex.RLock()
	defer knownDataSafesMutex.RUnlock()
	f, ok := knownDataSafes[name]
	return f, ok
}

// RegisterPasswordMethod registeres a password method.
// The name of the password method is used as an identifier and must be unique.
// You can savely use it in parallel.
func RegisterPasswordMethod(method PasswordMethod, name string) error {
	knownPasswordMethodsMutex.Lock()
	defer knownPasswordMethodsMutex.Unlock()

	_, ok := knownPasswordMethods[name]
	if ok {
		return AlreadyRegisteredError("PasswordMethod already registered")
	}
	knownPasswordMethods[name] = method
	return nil
}

// RegisterPasswordMethod returns whether the password method is known.
// You can savely use it in parallel.
func PasswordMethodExists(method string) bool {
	knownPasswordMethodsMutex.RLock()
	defer knownPasswordMethodsMutex.RUnlock()
	_, ok := knownPasswordMethods[method]
	return ok
}

// ComparePasswords compares a password to a 'truth'.
// The bool represents whether comparison is successful. Error is returned if there is any error during computation.
// You can savely use it in parallel.
func ComparePasswords(method, password, truth string) (bool, error) {
	knownPasswordMethodsMutex.RLock()
	defer knownPasswordMethodsMutex.RUnlock()
	f, ok := knownPasswordMethods[method]
	if !ok {
		return false, fmt.Errorf("Unknown password method %s", method)
	}
	return f(password, truth)
}
