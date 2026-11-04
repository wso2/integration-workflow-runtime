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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

var jsonOptions = protojson.MarshalOptions{Indent: "  ", EmitUnpopulated: false}

// writeJSON writes one message as an object, or several as an array, in the Temporal API's JSON form.
func writeJSON(w io.Writer, list bool, msgs ...proto.Message) error {
	raw := make([]json.RawMessage, len(msgs))
	for i, m := range msgs {
		b, err := jsonOptions.Marshal(m)
		if err != nil {
			return err
		}
		raw[i] = b
	}
	var out []byte
	var err error
	if list {
		out, err = json.MarshalIndent(raw, "", "  ")
	} else if len(raw) == 1 {
		var buf bytes.Buffer
		err = json.Indent(&buf, raw[0], "", "  ")
		out = buf.Bytes()
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(out))
	return err
}

// table writes rows with aligned columns; the first row is the header.
func table(w io.Writer, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(r, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// duration shows a retention in days when it is a whole number of days, as namespaces are usually set.
func duration(d *durationpb.Duration) string {
	if d == nil {
		return ""
	}
	v := d.AsDuration()
	if v > 0 && v%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", v/(24*time.Hour))
	}
	return v.String()
}
