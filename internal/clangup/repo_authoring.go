package clangup

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/zhscn/clangup/internal/clangup/repository/authoring"
)

func newRepoPublishCommand() *cobra.Command {
	var namespace, displayName, defaultChannel, endpoint, bucket, catalogKey, format string
	var dryRun bool
	command := &cobra.Command{
		Use:   "publish <release.json>",
		Short: "Promote an uploaded release into a repository catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := validateOutputFormat(format); err != nil {
				return invalidRequest(err)
			}
			if namespace == "" {
				return invalidRequest(fmt.Errorf("--namespace is required"))
			}
			if displayName == "" {
				displayName = namespace
			}
			releasePath, err := filepath.Abs(args[0])
			if err != nil {
				return invalidRequest(err)
			}
			store := authoring.NewS3CLIStore(endpoint, bucket)
			result, err := authoring.Publish(store, releasePath, authoring.PublishOptions{
				Namespace: namespace, DisplayName: displayName, DefaultChannel: defaultChannel,
				CatalogKey: catalogKey, DryRun: dryRun,
			})
			if err != nil {
				return invalidRepository(err)
			}
			if format == "json" {
				return writeJSON(command, result)
			}
			if dryRun {
				fmt.Fprintf(command.OutOrStdout(), "catalog: %s@%s\n", result.Channel, result.Exact)
				return nil
			}
			fmt.Fprintf(command.OutOrStdout(), "published: %s@%s -> s3://%s/%s\n", result.Channel, result.Exact, bucket, catalogKey)
			return nil
		},
	}
	command.Flags().StringVar(&namespace, "namespace", "", "repository namespace")
	command.Flags().StringVar(&displayName, "display-name", "", "repository display name")
	command.Flags().StringVar(&defaultChannel, "default-channel", "", "repository default channel")
	command.Flags().StringVar(&endpoint, "endpoint", "", "S3-compatible endpoint URL")
	command.Flags().StringVar(&bucket, "bucket", "", "S3 bucket")
	command.Flags().StringVar(&catalogKey, "catalog-key", "catalog-v1.json", "catalog object key")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "validate and prepare the catalog without writing it")
	command.Flags().StringVar(&format, "format", "text", outputFormatHelp)
	_ = command.MarkFlagRequired("endpoint")
	_ = command.MarkFlagRequired("bucket")
	return command
}
