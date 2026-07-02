package cmd

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/aliyun/infraguard/pkg/i18n"
	"github.com/aliyun/infraguard/pkg/models"
	"github.com/aliyun/infraguard/pkg/policy"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"
	"github.com/spf13/cobra"
)

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "", // Set dynamically
	Long:  "", // Set dynamically
}

var (
	policyRepo    string
	policyVersion string
)

var policyUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "", // Set dynamically
	Long:  "", // Set dynamically
	RunE:  runPolicyUpdate,
}

var policyGetCmd = &cobra.Command{
	Use:          "get <ID>",
	Short:        "", // Set dynamically
	Long:         "", // Set dynamically
	Args:         cobra.ExactArgs(1),
	RunE:         runPolicyGet,
	SilenceUsage: true,
}

var policyListCmd = &cobra.Command{
	Use:   "list",
	Short: "", // Set dynamically
	Long:  "", // Set dynamically
	RunE:  runPolicyList,
}

var policyValidateCmd = &cobra.Command{
	Use:          "validate <path>",
	Short:        "", // Set dynamically
	Long:         "", // Set dynamically
	Args:         cobra.ExactArgs(1),
	RunE:         runPolicyValidate,
	SilenceUsage: true,
}

var policyFormatCmd = &cobra.Command{
	Use:          "format <path>",
	Short:        "", // Set dynamically
	Long:         "", // Set dynamically
	Args:         cobra.ExactArgs(1),
	RunE:         runPolicyFormat,
	SilenceUsage: true,
}

var policyCleanCmd = &cobra.Command{
	Use:          "clean",
	Short:        "", // Set dynamically
	Long:         "", // Set dynamically
	RunE:         runPolicyClean,
	SilenceUsage: true,
}

var (
	formatWrite bool
	formatDiff  bool
)

var (
	cleanForce bool
)

// Color definitions
var (
	boldColor   = color.New(color.Bold)
	greenColor  = color.New(color.FgGreen)
	redColor    = color.New(color.FgRed)
	yellowColor = color.New(color.FgYellow)
)

func init() {
	policyCmd.AddCommand(policyUpdateCmd)
	policyCmd.AddCommand(policyGetCmd)
	policyCmd.AddCommand(policyListCmd)
	policyCmd.AddCommand(policyValidateCmd)
	policyCmd.AddCommand(policyFormatCmd)
	policyCmd.AddCommand(policyCleanCmd)

	// Flag descriptions - using English as default since init runs before language detection
	policyUpdateCmd.Flags().StringVar(&policyRepo, "repo", policy.DefaultRepo,
		"Git repository URL or path; empty uses the default OSS policy archive")
	policyUpdateCmd.Flags().StringVar(&policyVersion, "version", policy.DefaultPolicyVersion,
		"Policy version to download; latest reads the OSS version file, or main when --repo is set")

	// Format command flags
	policyFormatCmd.Flags().BoolVarP(&formatWrite, "write", "w", false,
		"Write formatted output to files")
	policyFormatCmd.Flags().BoolVarP(&formatDiff, "diff", "d", false,
		"Show diff output only")

	// Clean command flags
	policyCleanCmd.Flags().BoolVarP(&cleanForce, "force", "f", false,
		"Skip confirmation and clean directly")
}

func runPolicyUpdate(cmd *cobra.Command, args []string) error {
	msg := i18n.Msg()

	source := policyRepo
	version := policyVersion
	if strings.TrimSpace(source) == "" {
		source = "OSS"
	} else if strings.TrimSpace(version) == "" || version == policy.DefaultPolicyVersion {
		version = policy.DefaultPolicyGitRef
	}
	fmt.Printf(msg.PolicyUpdate.Progress+"\n", source, version)

	pm := policy.NewManager(policy.DefaultPolicyDir())
	if err := pm.Update(policyRepo, policyVersion); err != nil {
		return fmt.Errorf(msg.Errors.UpdatePolicies, err)
	}

	fmt.Println(msg.PolicyUpdate.Success)
	return nil
}

func runPolicyGet(cmd *cobra.Command, args []string) error {
	msg := i18n.Msg()
	lang := i18n.GetLanguage()
	id := args[0]

	// Load policies with fallback
	loader, err := policy.LoadWithFallback()
	if err != nil {
		return fmt.Errorf(msg.Errors.PolicyDir, err)
	}

	// Auto-detect type based on ID prefix
	if strings.HasPrefix(id, "rule:") {
		rule := loader.GetRule(id)
		if rule == nil {
			return fmt.Errorf(msg.PolicyGet.NotFoundRule, id)
		}
		printRuleDetails(rule, lang, msg)
	} else if strings.HasPrefix(id, "pack:") {
		pack := loader.GetPack(id)
		if pack == nil {
			return fmt.Errorf(msg.PolicyGet.NotFoundPack, id)
		}
		printPackDetails(pack, loader, lang, msg)
	} else {
		// Try rule first, then pack
		rule := loader.GetRule(id)
		if rule != nil {
			printRuleDetails(rule, lang, msg)
			return nil
		}
		pack := loader.GetPack(id)
		if pack != nil {
			printPackDetails(pack, loader, lang, msg)
			return nil
		}
		return fmt.Errorf(msg.Errors.PolicyNotFound, id)
	}

	return nil
}

func runPolicyList(cmd *cobra.Command, args []string) error {
	msg := i18n.Msg()
	lang := i18n.GetLanguage()

	// Load policies with fallback
	loader, err := policy.LoadWithFallback()
	if err != nil {
		return fmt.Errorf(msg.Errors.PolicyDir, err)
	}

	rules := loader.GetAllRules()
	packs := loader.GetAllPacks()

	if len(rules) == 0 && len(packs) == 0 {
		fmt.Println(msg.PolicyGet.NoRulesLoaded)
		return nil
	}

	// Print packs first
	if len(packs) > 0 {
		// Sort packs by ID
		sort.Slice(packs, func(i, j int) bool {
			return packs[i].ID < packs[j].ID
		})

		boldColor.Printf("\n%s (%d)\n\n", msg.PolicyList.Packs, len(packs))

		table := tablewriter.NewTable(os.Stdout,
			tablewriter.WithRendition(tw.Rendition{
				Settings: tw.Settings{
					Separators: tw.Separators{
						BetweenRows: tw.On,
					},
				},
			}),
		)
		table.Header(msg.PolicyGet.PackID, msg.PolicyGet.Name, msg.PolicyList.Rules)

		for _, pack := range packs {
			rulesCount := fmt.Sprintf("%d", len(pack.RuleIDs))
			table.Append(pack.ID, wrapText(pack.Name.Get(lang), 40), rulesCount)
		}

		table.Render()
	}

	// Print rules
	if len(rules) > 0 {
		// Sort rules by severity (High -> Medium -> Low), then by ID
		sortRules(rules)

		boldColor.Printf("\n%s (%d)\n\n", msg.PolicyList.Rules, len(rules))

		table := tablewriter.NewTable(os.Stdout,
			tablewriter.WithRendition(tw.Rendition{
				Settings: tw.Settings{
					Separators: tw.Separators{
						BetweenRows: tw.On,
					},
				},
			}),
		)
		table.Header(msg.Report.RuleID, msg.PolicyGet.Name, msg.Report.Severity, msg.PolicyGet.IaCTypes, msg.PolicyGet.ResourceTypes)

		for _, rule := range rules {
			resourceTypes := formatResourceTypes(rule)
			iacTypes := formatIaCTypes(rule.IaCTypes)
			table.Append(wrapText(rule.ID, 20), wrapText(rule.Name.Get(lang), 40), formatSeverityWithColor(rule.Severity, lang), iacTypes, wrapText(resourceTypes, 30))
		}

		table.Render()
	}

	fmt.Println()
	return nil
}

func printRuleDetails(rule *models.Rule, lang string, msg *i18n.Messages) {
	// Title
	boldColor.Printf("\n%s\n\n", msg.PolicyGet.RuleDetails)

	// Print details as vertical table (field | value)
	table := tablewriter.NewTable(os.Stdout,
		tablewriter.WithRendition(tw.Rendition{
			Settings: tw.Settings{
				Separators: tw.Separators{
					BetweenRows: tw.On,
				},
			},
		}),
	)
	table.Append(msg.Report.RuleID, rule.ID)
	table.Append(msg.PolicyGet.Name, wrapText(rule.Name.Get(lang), 80))
	table.Append(msg.Report.Severity, formatSeverityWithColor(rule.Severity, lang))
	table.Append(msg.PolicyGet.Description, wrapText(rule.Description.Get(lang), 80))
	table.Append(msg.PolicyGet.IaCTypes, formatIaCTypes(rule.IaCTypes))
	table.Append(msg.PolicyGet.ResourceTypes, formatResourceTypes(rule))
	table.Render()

	fmt.Println()
}

func formatIaCTypes(iacTypes []string) string {
	var parts []string
	for _, t := range iacTypes {
		switch t {
		case "ros":
			parts = append(parts, "ROS")
		case "terraform":
			parts = append(parts, "Terraform")
		default:
			parts = append(parts, t)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "/")
}

func formatResourceTypes(rule *models.Rule) string {
	if len(rule.IaCTypes) <= 1 {
		return strings.Join(rule.ResourceTypes, ", ")
	}
	var parts []string
	for _, iacType := range rule.IaCTypes {
		types := getResourceTypesForImpl(rule, iacType)
		if types != "" {
			label := iacType
			switch iacType {
			case "ros":
				label = "ROS"
			case "terraform":
				label = "Terraform"
			}
			parts = append(parts, fmt.Sprintf("%s: %s", label, types))
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func getResourceTypesForImpl(rule *models.Rule, iacType string) string {
	var types []string
	for _, rt := range rule.ResourceTypes {
		if iacType == "terraform" && strings.HasPrefix(rt, "alicloud_") {
			types = append(types, rt)
		} else if iacType == "ros" && strings.HasPrefix(rt, "ALIYUN::") {
			types = append(types, rt)
		}
	}
	if len(types) == 0 {
		return strings.Join(rule.ResourceTypes, ", ")
	}
	return strings.Join(types, ", ")
}

func printPackDetails(pack *models.Pack, loader *policy.Loader, lang string, msg *i18n.Messages) {
	// Title
	boldColor.Printf("\n%s\n\n", msg.PolicyGet.PackDetails)

	// Print pack details as vertical table (field | value)
	table := tablewriter.NewTable(os.Stdout,
		tablewriter.WithRendition(tw.Rendition{
			Settings: tw.Settings{
				Separators: tw.Separators{
					BetweenRows: tw.On,
				},
			},
		}),
	)
	table.Append(msg.PolicyGet.PackID, pack.ID)
	table.Append(msg.PolicyGet.Name, wrapText(pack.Name.Get(lang), 80))
	table.Append(msg.PolicyGet.Description, wrapText(pack.Description.Get(lang), 80))
	table.Render()

	fmt.Println()

	// Display included rules
	rules := loader.GetRulesForPack(pack.ID)
	if len(rules) > 0 {
		// Sort rules by severity, then by ID
		sortRules(rules)

		boldColor.Printf("%s (%d)\n\n", msg.PolicyGet.IncludedRules, len(rules))

		rulesTable := tablewriter.NewTable(os.Stdout,
			tablewriter.WithRendition(tw.Rendition{
				Settings: tw.Settings{
					Separators: tw.Separators{
						BetweenRows: tw.On,
					},
				},
			}),
		)
		rulesTable.Header(msg.Report.RuleID, msg.PolicyGet.Name, msg.Report.Severity, msg.PolicyGet.IaCTypes, msg.PolicyGet.ResourceTypes)

		for _, rule := range rules {
			resourceTypes := formatResourceTypes(rule)
			iacTypes := formatIaCTypes(rule.IaCTypes)
			rulesTable.Append(wrapText(rule.ID, 20), wrapText(rule.Name.Get(lang), 40), formatSeverityWithColor(rule.Severity, lang), iacTypes, wrapText(resourceTypes, 30))
		}

		rulesTable.Render()
	} else if len(pack.RuleIDs) > 0 {
		// Show rule IDs that couldn't be resolved
		fmt.Printf(msg.PolicyGet.IncludedRulesFormat+"\n", msg.PolicyGet.IncludedRules, strings.Join(pack.RuleIDs, ", "))
	}

	fmt.Println()
}

// sortRules sorts rules by severity (High -> Medium -> Low), then by ID ascending
func sortRules(rules []*models.Rule) {
	sort.Slice(rules, func(i, j int) bool {
		// First compare by severity order
		severityOrder := map[string]int{
			models.SeverityHigh:   0,
			models.SeverityMedium: 1,
			models.SeverityLow:    2,
		}
		iOrder := severityOrder[strings.ToLower(rules[i].Severity)]
		jOrder := severityOrder[strings.ToLower(rules[j].Severity)]

		if iOrder != jOrder {
			return iOrder < jOrder
		}
		// Same severity, sort by ID ascending
		return rules[i].ID < rules[j].ID
	})
}

// formatSeverityWithColor returns severity text with color based on level
func formatSeverityWithColor(severity, lang string) string {
	msg := i18n.Msg()
	switch strings.ToLower(severity) {
	case models.SeverityHigh:
		return redColor.Sprint(msg.Severity.High)
	case models.SeverityMedium:
		return yellowColor.Sprint(msg.Severity.Medium)
	case models.SeverityLow:
		return greenColor.Sprint(msg.Severity.Low)
	default:
		return severity
	}
}

// runeDisplayWidth returns the display width of a rune.
// CJK characters (Chinese, Japanese, Korean) take 2 columns, others take 1.
func runeDisplayWidth(r rune) int {
	// CJK Unified Ideographs and common CJK ranges
	if r >= 0x4E00 && r <= 0x9FFF || // CJK Unified Ideographs
		r >= 0x3400 && r <= 0x4DBF || // CJK Unified Ideographs Extension A
		r >= 0x20000 && r <= 0x2A6DF || // CJK Unified Ideographs Extension B
		r >= 0x2A700 && r <= 0x2B73F || // CJK Unified Ideographs Extension C
		r >= 0x2B740 && r <= 0x2B81F || // CJK Unified Ideographs Extension D
		r >= 0xF900 && r <= 0xFAFF || // CJK Compatibility Ideographs
		r >= 0xFF00 && r <= 0xFFEF || // Fullwidth Forms
		r >= 0x3000 && r <= 0x303F { // CJK Symbols and Punctuation
		return 2
	}
	return 1
}

// stringDisplayWidth returns the display width of a string
func stringDisplayWidth(s string) int {
	width := 0
	for _, r := range s {
		width += runeDisplayWidth(r)
	}
	return width
}

// wrapText wraps text at specified width, breaking at word boundaries
// For long words without spaces, it breaks at delimiters (- or :) or forces character breaks
// Properly handles UTF-8 characters including CJK characters
func wrapText(text string, width int) string {
	if width <= 0 || stringDisplayWidth(text) <= width {
		return text
	}

	var result strings.Builder
	words := strings.Fields(text)
	lineLen := 0

	for _, word := range words {
		wordWidth := stringDisplayWidth(word)

		// Handle long words that exceed width
		if wordWidth > width {
			if lineLen > 0 {
				result.WriteString("\n")
				lineLen = 0
			}
			wrappedWord := wrapLongWord(word, width)
			result.WriteString(wrappedWord)
			// Calculate the display width of the last line
			lastNewline := strings.LastIndex(wrappedWord, "\n")
			if lastNewline >= 0 {
				lineLen = stringDisplayWidth(wrappedWord[lastNewline+1:])
			} else {
				lineLen = stringDisplayWidth(wrappedWord)
			}
			continue
		}

		if lineLen == 0 {
			// First word on line
			result.WriteString(word)
			lineLen = wordWidth
		} else if lineLen+1+wordWidth <= width {
			// Word fits on current line
			result.WriteString(" ")
			result.WriteString(word)
			lineLen += 1 + wordWidth
		} else {
			// Need to wrap
			result.WriteString("\n")
			result.WriteString(word)
			lineLen = wordWidth
		}
	}

	return result.String()
}

// wrapLongWord breaks a long word at delimiters (- or :) or forces character breaks
// Properly handles UTF-8 characters including CJK characters
func wrapLongWord(word string, width int) string {
	var result strings.Builder
	lineLen := 0
	runes := []rune(word)

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		charWidth := runeDisplayWidth(r)
		result.WriteRune(r)
		lineLen += charWidth

		// Check if we should break after this character
		if lineLen >= width && i < len(runes)-1 {
			// Prefer breaking after delimiters
			if r == '-' || r == ':' {
				result.WriteString("\n")
				lineLen = 0
			} else {
				// Look ahead for a delimiter within a few characters
				breakFound := false
				for j := i + 1; j < len(runes) && j <= i+5; j++ {
					if runes[j] == '-' || runes[j] == ':' {
						// Write characters up to and including the delimiter
						for k := i + 1; k <= j; k++ {
							result.WriteRune(runes[k])
							lineLen += runeDisplayWidth(runes[k])
						}
						result.WriteString("\n")
						lineLen = 0
						i = j
						breakFound = true
						break
					}
				}
				// If no delimiter found, force break
				if !breakFound {
					result.WriteString("\n")
					lineLen = 0
				}
			}
		}
	}

	return result.String()
}

func runPolicyValidate(cmd *cobra.Command, args []string) error {
	msg := i18n.Msg()
	path := args[0]

	summary, err := policy.ValidatePolicies(path)
	if err != nil {
		return err
	}

	// Print skipped files first (if any)
	if summary.SkippedFiles > 0 {
		fmt.Printf(msg.PolicyValidate.SkippedSummary+"\n", summary.SkippedFiles)
		for _, skipped := range summary.Skipped {
			yellowColor.Printf(msg.PolicyValidate.SkippedPrefix+"%s\n", skipped)
		}
		fmt.Println()
	}

	if summary.TotalFiles == 0 {
		fmt.Println(msg.PolicyValidate.NoFilesFound)
		return nil
	}

	// Print results
	hasErrors := false
	for _, result := range summary.Results {
		if !result.Valid {
			hasErrors = true
			printValidationErrors(result, msg)
		}
	}

	// Print summary
	fmt.Println()
	if hasErrors {
		redColor.Printf(msg.PolicyValidate.Summary+"\n", summary.TotalFiles, summary.PassedFiles, summary.FailedFiles)
		return fmt.Errorf("%s", msg.PolicyValidate.Failed)
	}

	greenColor.Printf(msg.PolicyValidate.Passed+"\n", summary.TotalFiles)
	return nil
}

func printValidationErrors(result *policy.ValidationResult, msg *i18n.Messages) {
	// Translate file type
	fileType := result.FileType
	switch result.FileType {
	case "rule":
		fileType = msg.PolicyValidate.FileTypeRule
	case "pack":
		fileType = msg.PolicyValidate.FileTypePack
	case "unknown":
		fileType = msg.PolicyValidate.FileTypeUnknown
	}
	boldColor.Printf("\n%s (%s)\n", result.FilePath, fileType)

	for _, err := range result.Errors {
		// Get localized message, fallback to English message
		message := err.Message
		if localizedMsg, ok := msg.PolicyValidate.Errors[err.ErrorCode]; ok && localizedMsg != "" {
			message = localizedMsg
		}

		// Get localized suggestion, fallback to English suggestion
		suggestion := err.Suggestion
		suggestionKey := err.ErrorCode + "_suggestion"
		if localizedSugg, ok := msg.PolicyValidate.Errors[suggestionKey]; ok && localizedSugg != "" {
			suggestion = localizedSugg
		}

		// Error message
		redColor.Printf(msg.PolicyValidate.ErrorPrefix+"[%s] %s\n", err.ErrorCode, message)
		// Suggestion
		if suggestion != "" {
			yellowColor.Printf(msg.PolicyValidate.SuggestionPrefix+"%s: %s\n", msg.PolicyValidate.Suggestion, suggestion)
		}
	}
}

func runPolicyFormat(cmd *cobra.Command, args []string) error {
	msg := i18n.Msg()
	path := args[0]

	summary, err := policy.FormatPath(path, formatWrite)
	if err != nil {
		return fmt.Errorf(msg.Errors.PolicyDir, err)
	}

	if summary.TotalFiles == 0 {
		fmt.Println(msg.PolicyFormat.NoFilesFound)
		return nil
	}

	// Print results - only show changed files and errors
	hasOutput := false
	for _, result := range summary.Results {
		if result.Error != nil {
			redColor.Printf(msg.PolicyFormat.ErrorPrefix+"%s: %v\n", result.FilePath, result.Error)
			hasOutput = true
			continue
		}

		if result.Changed {
			hasOutput = true
			if formatWrite {
				greenColor.Printf(msg.PolicyFormat.SuccessPrefix+"%s: %s\n", msg.PolicyFormat.Formatted, result.FilePath)
			} else if formatDiff {
				fmt.Print(policy.GenerateDiff(result.Original, result.Formatted, result.FilePath))
			} else {
				yellowColor.Printf(msg.PolicyFormat.NeedsFormatPrefix+"%s: %s\n", msg.PolicyFormat.NeedsFormatting, result.FilePath)
			}
		}
		// Skip unchanged files in output
	}

	// Print summary - only show non-zero counts
	// Only add blank line if there was output above
	if hasOutput {
		fmt.Println()
	}
	if summary.ChangedFiles > 0 {
		fmt.Printf(msg.PolicyFormat.SummaryChanged+"\n", summary.ChangedFiles)
	}
	if summary.UnchangedFiles > 0 {
		fmt.Printf(msg.PolicyFormat.SummaryUnchanged+"\n", summary.UnchangedFiles)
	}

	// Exit with error if files need formatting and not in write mode
	if summary.ChangedFiles > 0 && !formatWrite {
		return fmt.Errorf("%s", msg.PolicyFormat.NeedsFormat)
	}

	return nil
}

func runPolicyClean(cmd *cobra.Command, args []string) error {
	msg := i18n.Msg()
	policyDir := policy.DefaultPolicyDir()

	// If not using --force, show confirmation prompt
	if !cleanForce {
		fmt.Printf(msg.PolicyClean.Confirm, policyDir)

		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			// EOF or error reading input - treat as cancel
			fmt.Println(msg.PolicyClean.Cancelled)
			return nil
		}

		input := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if input != "y" && input != "yes" {
			fmt.Println(msg.PolicyClean.Cancelled)
			return nil
		}
	}

	// Perform the clean operation
	fmt.Println(msg.PolicyClean.Progress)

	pm := policy.NewManager(policyDir)
	if err := pm.Clean(); err != nil {
		return err
	}

	// Check if directory existed (if Clean succeeded but directory is gone, it existed)
	if _, err := os.Stat(policyDir); os.IsNotExist(err) {
		// Directory was cleaned or didn't exist
		if cleanForce {
			// In force mode, directory might not have existed
			fmt.Println(msg.PolicyClean.Success)
		} else {
			// User confirmed, so directory existed and was cleaned
			fmt.Println(msg.PolicyClean.Success)
		}
	}

	return nil
}
