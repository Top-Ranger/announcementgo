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

package counter

import (
	"fmt"
	"sync"
	"time"
)

var processCount int64
var processCountMutex sync.Mutex

// StartProcess increases the process counter.
func StartProcess() {
	processCountMutex.Lock()
	defer processCountMutex.Unlock()
	processCount++
}

// EndProcess decreases the process counter.
func EndProcess() {
	processCountMutex.Lock()
	defer processCountMutex.Unlock()
	processCount--
}

// WaitProcesses blocks until all processes have finished.
func WaitProcesses() {
	for {
		processCountMutex.Lock()
		fmt.Println(processCount)
		r := processCount == 0
		processCountMutex.Unlock()
		if r {
			return
		}
		time.Sleep(1 * time.Second)
	}
}
