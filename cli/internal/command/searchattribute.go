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
	"slices"
	"strings"

	"github.com/spf13/cobra"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
)

// searchAttributeTypes are the --type values, as the Temporal CLI spells them.
var searchAttributeTypes = map[string]enumspb.IndexedValueType{
	"Text":        enumspb.INDEXED_VALUE_TYPE_TEXT,
	"Keyword":     enumspb.INDEXED_VALUE_TYPE_KEYWORD,
	"Int":         enumspb.INDEXED_VALUE_TYPE_INT,
	"Double":      enumspb.INDEXED_VALUE_TYPE_DOUBLE,
	"Bool":        enumspb.INDEXED_VALUE_TYPE_BOOL,
	"Datetime":    enumspb.INDEXED_VALUE_TYPE_DATETIME,
	"KeywordList": enumspb.INDEXED_VALUE_TYPE_KEYWORD_LIST,
}

func typeName(t enumspb.IndexedValueType) string {
	for name, v := range searchAttributeTypes {
		if v == t {
			return name
		}
	}
	return t.String()
}

func (c *cli) searchAttributeCommand() *cobra.Command {
	sa := &cobra.Command{Use: "search-attribute", Short: "Add and list a namespace's search attributes"}
	sa.AddCommand(c.searchAttributeCreateCommand(), c.searchAttributeListCommand())
	return sa
}

func (c *cli) searchAttributeCreateCommand() *cobra.Command {
	var names, types []string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Add search attributes to a namespace",
		Long: "Adds each --name with the --type in the same position, for example\n" +
			"  workflow-cli operator search-attribute create -n orders --name Customer --type Keyword\n" +
			"An attribute that exists already is left as it is.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(names) == 0 || len(names) != len(types) {
				return errors.New("give each --name a --type, in the same order")
			}
			attrs := map[string]enumspb.IndexedValueType{}
			for i, name := range names {
				t, ok := searchAttributeTypes[types[i]]
				if !ok {
					return fmt.Errorf("--type %q must be one of %s", types[i], choices(searchAttributeTypes))
				}
				attrs[name] = t
			}
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			if err := addSearchAttributes(ctx, operatorservice.NewOperatorServiceClient(conn), s.Namespace,
				attrs); err != nil {
				return err
			}
			format := "Search attributes %s are in namespace %s.\n"
			if len(names) == 1 {
				format = "Search attribute %s is in namespace %s.\n"
			}
			return c.confirm(cmd, format, strings.Join(names, ", "), s.Namespace)
		},
	}
	cmd.Flags().StringArrayVar(&names, "name", nil, "the attribute's name; repeat for more")
	cmd.Flags().StringArrayVar(&types, "type", nil,
		"the attribute's type: Text, Keyword, Int, Double, Bool, Datetime or KeywordList")
	return cmd
}

func (c *cli) searchAttributeListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List a namespace's search attributes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			res, err := operatorservice.NewOperatorServiceClient(conn).ListSearchAttributes(ctx,
				&operatorservice.ListSearchAttributesRequest{Namespace: s.Namespace})
			if err != nil {
				return fmt.Errorf("unable to list the search attributes of %s: %w", s.Namespace, err)
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, false, res)
			}
			rows := [][]string{{"NAME", "TYPE", "KIND"}}
			add := func(kind string, attrs map[string]enumspb.IndexedValueType) {
				names := make([]string, 0, len(attrs))
				for n := range attrs {
					names = append(names, n)
				}
				slices.Sort(names)
				for _, n := range names {
					rows = append(rows, []string{n, typeName(attrs[n]), kind})
				}
			}
			add("custom", res.GetCustomAttributes())
			add("system", res.GetSystemAttributes())
			return table(out, rows)
		},
	}
}
