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

package command

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/wso2/integration-workflow-runtime/cli/internal/clientconfig"
)

// secretProperty is shown masked by `config get`.
const secretProperty = "apiKey"

func (c *cli) configCommand() *cobra.Command {
	cfg := &cobra.Command{
		Use:   "config",
		Short: "Manage connection profiles",
		Long: "Profiles store how to reach an engine, one per environment, in the profile file.\n" +
			"Properties: " + strings.Join(clientconfig.Properties(), ", ") + ".",
	}
	cfg.AddCommand(c.configGetCommand(), c.configSetCommand(), c.configDeleteCommand(), c.configDeleteProfileCommand(),
		c.configListCommand())
	return cfg
}

func (c *cli) openConfig() (*clientconfig.File, string, error) {
	path, err := c.configPath()
	if err != nil {
		return nil, "", err
	}
	f, err := clientconfig.Open(path)
	return f, path, err
}

func (c *cli) configGetCommand() *cobra.Command {
	var prop string
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Show a profile, or one property of it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, path, err := c.openConfig()
			if err != nil {
				return err
			}
			profile := c.profileName()
			if !f.Has(profile) {
				return fmt.Errorf("no profile %q in %s", profile, path)
			}
			out := cmd.OutOrStdout()
			if prop != "" {
				v, err := f.Get(profile, prop)
				if err != nil {
					return err
				}
				if c.output == outputJSON {
					return printJSON(out, map[string]string{prop: v})
				}
				_, err = fmt.Fprintln(out, v)
				return err
			}
			rows, values := [][]string{{"PROPERTY", "VALUE"}}, map[string]string{}
			for _, p := range clientconfig.Properties() {
				v, _ := f.Get(profile, p)
				if v == "" {
					continue
				}
				if p == secretProperty {
					v = "(set)"
				}
				rows = append(rows, []string{p, v})
				values[p] = v
			}
			if c.output == outputJSON {
				return printJSON(out, values)
			}
			return table(out, rows)
		},
	}
	cmd.Flags().StringVar(&prop, "prop", "", "the property to show")
	return cmd
}

func (c *cli) configSetCommand() *cobra.Command {
	var prop, value string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set one property of a profile, creating the profile when needed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if prop == "" || !cmd.Flags().Changed("value") {
				return errors.New("set both --prop and --value")
			}
			f, path, err := c.openConfig()
			if err != nil {
				return err
			}
			if err := f.Set(c.profileName(), prop, value); err != nil {
				return err
			}
			return c.confirm(cmd, "Set %s in profile %s (%s).\n", prop, c.profileName(), path)
		},
	}
	cmd.Flags().StringVar(&prop, "prop", "", "the property to set")
	cmd.Flags().StringVar(&value, "value", "", "its value")
	return cmd
}

func (c *cli) configDeleteCommand() *cobra.Command {
	var prop string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove one property of a profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if prop == "" {
				return errors.New("set --prop; delete-profile removes a whole profile")
			}
			f, _, err := c.openConfig()
			if err != nil {
				return err
			}
			if err := f.Delete(c.profileName(), prop); err != nil {
				return err
			}
			return c.confirm(cmd, "Removed %s from profile %s.\n", prop, c.profileName())
		},
	}
	cmd.Flags().StringVar(&prop, "prop", "", "the property to remove")
	return cmd
}

func (c *cli) configDeleteProfileCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "delete-profile",
		Short: "Remove a profile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, _, err := c.openConfig()
			if err != nil {
				return err
			}
			if err := f.Delete(c.profileName(), ""); err != nil {
				return err
			}
			return c.confirm(cmd, "Removed profile %s.\n", c.profileName())
		},
	}
}

func (c *cli) configListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the profiles",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, _, err := c.openConfig()
			if err != nil {
				return err
			}
			if c.output == outputJSON {
				return printJSON(cmd.OutOrStdout(), f.Profiles())
			}
			for _, p := range f.Profiles() {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), p); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
