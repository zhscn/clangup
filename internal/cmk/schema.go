package cmk

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// SchemaURL is the canonical JSON Schema URL emitted in scaffolded cmk.yaml
// files for yaml-language-server and JetBrains YAML support.
const SchemaURL = "https://raw.githubusercontent.com/zhscn/clangup/main/internal/cmk/schema/cmk.schema.json"

const schemaModeline = "# yaml-language-server: $schema=" + SchemaURL

//go:embed schema/cmk.schema.json
var cmkJSONSchema []byte

func newSchemaCommand() *cobra.Command {
	var output string
	command := &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for cmk.yaml",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if output == "" {
				_, err := command.OutOrStdout().Write(cmkJSONSchema)
				return err
			}
			if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(output, cmkJSONSchema, 0o644); err != nil {
				return err
			}
			fmt.Fprintln(command.ErrOrStderr(), "cmk: wrote", output)
			return nil
		},
	}
	command.Flags().StringVarP(&output, "output", "o", "", "Write the schema to a file instead of stdout")
	return command
}
