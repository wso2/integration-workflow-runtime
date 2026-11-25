// Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package cli

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wso2/integration-workflow-runtime/engine/internal/runtimeconfig"
	"github.com/wso2/integration-workflow-runtime/engine/plugins/apikeys"
)

//nolint:gosec // usage text, not a credential
const apiKeyUsage = `usage: workflow-engine api-key create --runtime-config <runtime.toml> --name <name> --namespace <name>... --role read|write|admin [--expires <lifetime>]
       workflow-engine api-key list --runtime-config <runtime.toml>
       workflow-engine api-key revoke --runtime-config <runtime.toml> (--id <id> | --name <name>) [--reason <text>]`

func (c command) apiKey(args []string) error {
	if len(args) == 0 {
		return errors.New(apiKeyUsage)
	}
	switch args[0] {
	case "create":
		return c.apiKeyCreate(args[1:])
	case "list":
		return c.apiKeyList(args[1:])
	case "revoke":
		return c.apiKeyRevoke(args[1:])
	}
	return errors.New(apiKeyUsage)
}

// apiKeyFiles reads runtime.toml and returns the API key files it names.
func apiKeyFiles(path string) (*runtimeconfig.APIKeys, error) {
	if path == "" {
		return nil, errors.New("--runtime-config is required")
	}
	rt, err := runtimeconfig.Load(path)
	if err != nil {
		return nil, err
	}
	if rt.APIKeys == nil {
		return nil, fmt.Errorf("%s has no [engine.auth.apiKeys]; set signingKey there first", path)
	}
	return rt.APIKeys, nil
}

func (c command) apiKeyCreate(args []string) error {
	flags := flag.NewFlagSet("api-key create", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	runtimeConfig := flags.String("runtime-config", "", "the runtime's runtime.toml")
	name := flags.String("name", "", "who holds the key, such as an integration's name")
	var namespaces serviceList
	flags.Var(&namespaces, "namespace", "a namespace the key grants the role in, repeatable; * for every namespace")
	role := flags.String("role", "", "read, write (an integration's workers need write) or admin")
	// A year by default: long enough for a running integration, short enough that a forgotten key ends.
	expires := flags.String("expires", "365d", "the key's lifetime, such as 90d or 720h")
	if err := flags.Parse(args); err != nil {
		return err
	}
	files, err := apiKeyFiles(*runtimeConfig)
	if err != nil {
		return err
	}
	ttl, err := parseLifetime(*expires)
	if err != nil {
		return err
	}
	if len(namespaces) == 0 {
		return errors.New("give at least one --namespace, or * for every namespace")
	}
	grants := make([]apikeys.Grant, len(namespaces))
	for i, ns := range namespaces {
		grants[i] = apikeys.Grant{Namespace: ns, Role: *role}
	}
	key, created, err := apikeys.LoadKey(files.SigningKey, true)
	if err != nil {
		return err
	}
	if created {
		_, _ = fmt.Fprintf(c.stderr, "Created the signing key %s; copy it to every node of this runtime.\n",
			files.SigningKey)
	}
	issued, err := key.Create(*name, grants, ttl, time.Now())
	if err != nil {
		return err
	}
	if err := apikeys.Record(files.IssuedRecord, issued); err != nil {
		return fmt.Errorf("unable to record the key: %w", err)
	}
	_, _ = fmt.Fprintf(c.stderr, "API key %s for %s: %s in %s, expires %s. It is shown only once.\n", issued.ID,
		issued.Name, strings.ToLower(*role), strings.Join(namespaces, ", "), issued.Expires.Format(time.DateOnly))
	_, err = fmt.Fprintln(c.stdout, issued.Token)
	return err
}

func (c command) apiKeyList(args []string) error {
	flags := flag.NewFlagSet("api-key list", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	runtimeConfig := flags.String("runtime-config", "", "the runtime's runtime.toml")
	if err := flags.Parse(args); err != nil {
		return err
	}
	files, err := apiKeyFiles(*runtimeConfig)
	if err != nil {
		return err
	}
	entries, err := apikeys.ReadRecord(files.IssuedRecord)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		_, err := fmt.Fprintln(c.stdout, "No API key has been created.")
		return err
	}
	revoked, err := apikeys.ReadRevoked(files.RevocationList)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tGRANTS\tCREATED\tEXPIRES\tSTATE")
	now := time.Now()
	for _, e := range entries {
		state := "active"
		switch {
		case revoked[e.ID]:
			state = "revoked"
		case !now.Before(e.Expires):
			state = "expired"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.ID, e.Name, e.Grants, e.Created.Format(time.DateOnly),
			e.Expires.Format(time.DateOnly), state)
	}
	return w.Flush()
}

func (c command) apiKeyRevoke(args []string) error {
	flags := flag.NewFlagSet("api-key revoke", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	runtimeConfig := flags.String("runtime-config", "", "the runtime's runtime.toml")
	id := flags.String("id", "", "the key's ID, from api-key list")
	name := flags.String("name", "", "revoke every key with this name instead")
	reason := flags.String("reason", "", "why, kept in the revocation list")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*id == "") == (*name == "") {
		return errors.New("give --id or --name")
	}
	files, err := apiKeyFiles(*runtimeConfig)
	if err != nil {
		return err
	}
	ids := []string{*id}
	if *name != "" {
		entries, err := apikeys.ReadRecord(files.IssuedRecord)
		if err != nil {
			return err
		}
		ids = nil
		for _, e := range entries {
			if e.Name == *name {
				ids = append(ids, e.ID)
			}
		}
		if len(ids) == 0 {
			return fmt.Errorf("no API key named %q in %s", *name, files.IssuedRecord)
		}
	}
	for _, k := range ids {
		if err := apikeys.Revoke(files.RevocationList, k, *reason); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(c.stderr, "Revoked %s in %s; copy it to every node, which reads it within seconds.\n",
		strings.Join(ids, ", "), files.RevocationList)
	return err
}

// maxLifetimeDays is the most days a time.Duration holds, about 292 years.
const maxLifetimeDays = int(math.MaxInt64 / int64(24*time.Hour))

// parseLifetime reads a Go duration or a number of days ("90d").
func parseLifetime(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		// A day count past what time.Duration holds would wrap to a shorter lifetime.
		if err == nil && n > 0 && n <= maxLifetimeDays {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	} else if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d, nil
	}
	return 0, fmt.Errorf("--expires %q must be a positive lifetime such as 90d or 720h", s)
}
