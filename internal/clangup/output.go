package clangup

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

const outputFormatHelp = "output format: text or json"

func validateOutputFormat(format string) error {
	if format != "text" && format != "json" {
		return fmt.Errorf("unsupported format %q: expected text or json", format)
	}
	return nil
}

func writeJSON(command *cobra.Command, value any) error {
	encoder := json.NewEncoder(command.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
