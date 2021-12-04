// SPDX-License-Identifier: Apache-2.0
// Copyright 2021 Marcus Soll
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
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	_ "github.com/go-sql-driver/mysql"

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/registry"
)

func init() {
	m := new(mysql)

	err := registry.RegisterDataSafe(m, "MySQL")
	if err != nil {
		panic(err)
	}
}

// MySQLMaxLengthID is the maximum supported id length
const MySQLMaxLengthID = 500

// ErrMySQLUnknownID is returned when the requested id is too long
var ErrMySQLIDtooLong = errors.New("mysql: id is too long")

// ErrMySQLNotConfigured is returned when the database is used before it is configured
var ErrMySQLNotConfigured = errors.New("mysql: usage before configuration is used")

type mysql struct {
	dsn string
	db  *sql.DB
}

func (m *mysql) InitialiseDatasafe(config []byte) error {
	counter.StartProcess()
	defer counter.EndProcess()
	m.dsn = string(config)
	db, err := sql.Open("mysql", m.dsn)
	if err != nil {
		return fmt.Errorf("mysql: can not open '%s': %w", m.dsn, err)
	}
	m.db = db
	return nil
}

func (m *mysql) GetConfig(key, plugin string) ([]byte, error) {
	if m.db == nil {
		return nil, ErrMySQLNotConfigured
	}

	if len(key) > MySQLMaxLengthID {
		return nil, ErrMySQLIDtooLong
	}

	if len(plugin) > MySQLMaxLengthID {
		return nil, ErrMySQLIDtooLong
	}

	counter.StartProcess()
	defer counter.EndProcess()

	rows, err := m.db.Query("SELECT data FROM config WHERE k=? AND plugin=?", key, plugin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		// No config saved
		return nil, nil
	}
	var b []byte
	err = rows.Scan(&b)
	return b, err
}

func (m *mysql) SetConfig(key, plugin string, config []byte) error {
	if m.db == nil {
		return ErrMySQLNotConfigured
	}

	if len(key) > MySQLMaxLengthID {
		return ErrMySQLIDtooLong
	}

	if len(plugin) > MySQLMaxLengthID {
		return ErrMySQLIDtooLong
	}

	counter.StartProcess()
	defer counter.EndProcess()

	_, err := m.db.Exec("REPLACE INTO config (k, plugin, data) VALUES (?,?,?)", key, plugin, config)
	return err
}

func (m *mysql) SaveAnnouncement(key string, announcement registry.Announcement) (string, error) {
	if m.db == nil {
		return "", ErrMySQLNotConfigured
	}

	if len(key) > MySQLMaxLengthID {
		return "", ErrMySQLIDtooLong
	}

	counter.StartProcess()
	defer counter.EndProcess()

	r, err := m.db.Exec("INSERT INTO announcement (k, header, message, time ) VALUES (?,?,?,?)", key, announcement.Header, announcement.Message, announcement.Time)
	if err != nil {
		return "", err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

func (m *mysql) GetAnnouncement(key, id string) (registry.Announcement, error) {
	if m.db == nil {
		return registry.Announcement{}, ErrMySQLNotConfigured
	}

	if len(key) > MySQLMaxLengthID {
		return registry.Announcement{}, ErrMySQLIDtooLong
	}

	counter.StartProcess()
	defer counter.EndProcess()

	parsedId, err := strconv.ParseUint(key, 10, 64)
	if err != nil {
		return registry.Announcement{}, err
	}

	rows, err := m.db.Query("SELECT header, message, time FROM announcement WHERE id=?", parsedId)
	if err != nil {
		return registry.Announcement{}, err
	}
	defer rows.Close()

	if !rows.Next() {
		return registry.Announcement{}, nil
	}
	var a registry.Announcement
	err = rows.Scan(&a.Header, &a.Message, &a.Time)
	return a, err
}

func (m *mysql) GetAllAnnouncements(key string) ([]registry.Announcement, error) {
	if m.db == nil {
		return nil, ErrMySQLNotConfigured
	}

	if len(key) > MySQLMaxLengthID {
		return nil, ErrMySQLIDtooLong
	}

	counter.StartProcess()
	defer counter.EndProcess()

	rows, err := m.db.Query("SELECT header, message, time FROM announcement WHERE k=? ORDER BY id ASC", key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]registry.Announcement, 0)
	for rows.Next() {
		var a registry.Announcement
		err = rows.Scan(&a.Header, &a.Message, &a.Time)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, err
}

func (m *mysql) GetAnnouncementKeys(key string) ([]string, error) {
	if m.db == nil {
		return nil, ErrMySQLNotConfigured
	}

	if len(key) > MySQLMaxLengthID {
		return nil, ErrMySQLIDtooLong
	}

	counter.StartProcess()
	defer counter.EndProcess()

	rows, err := m.db.Query("SELECT id WHERE k=? ORDER BY id ASC", key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]string, 0)
	for rows.Next() {
		var i uint64
		err = rows.Scan(&i)
		if err != nil {
			return nil, err
		}
		result = append(result, strconv.FormatUint(i, 10))
	}
	return result, err
}
