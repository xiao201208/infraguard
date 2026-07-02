// Package template provides ROS template parsing and analysis.
package template

import (
	"encoding/json"
	"sort"
	"strings"

	goyaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	goyamlparser "github.com/goccy/go-yaml/parser"
	"github.com/kaptinlin/jsonrepair"
)

// ParsedTemplate represents a parsed ROS template with position information.
type ParsedTemplate struct {
	Content      string
	Format       string // "yaml" or "json"
	Root         map[string]interface{}
	YAMLFile     *ast.File
	TopLevelKeys []string
	Err          error
}

// ParseYAML parses a YAML document and returns a ParsedTemplate.
func ParseYAML(content string) *ParsedTemplate {
	pt := &ParsedTemplate{
		Content: content,
		Format:  "yaml",
	}

	file, err := goyamlparser.ParseBytes([]byte(content), 0)
	if err != nil {
		pt.Err = err
		// Try to parse as map anyway for partial results
		var root map[string]interface{}
		if err2 := goyaml.Unmarshal([]byte(content), &root); err2 == nil {
			pt.Root = root
			pt.TopLevelKeys = extractMapKeys(root)
		}
		return pt
	}

	pt.YAMLFile = file

	var root map[string]interface{}
	if err := goyaml.Unmarshal([]byte(content), &root); err != nil {
		pt.Err = err
		return pt
	}

	pt.Root = root
	pt.TopLevelKeys = extractMapKeys(root)
	return pt
}

// ParseJSON parses a JSON document and returns a ParsedTemplate.
func ParseJSON(content string) *ParsedTemplate {
	pt := &ParsedTemplate{
		Content: content,
		Format:  "json",
	}

	var root map[string]interface{}
	if err := json.Unmarshal([]byte(content), &root); err != nil {
		pt.Err = err
		if tryRepairJSON(content, &root) {
			pt.Root = root
			pt.TopLevelKeys = extractMapKeys(root)
		}
		return pt
	}

	pt.Root = root
	pt.TopLevelKeys = extractMapKeys(root)
	return pt
}

// tryRepairJSON attempts to repair invalid JSON and unmarshal into root.
// It first tries jsonrepair directly, then sanitizes lines with unclosed
// strings (common during editing) before retrying.
func tryRepairJSON(content string, root *map[string]interface{}) bool {
	repaired, err := jsonrepair.JSONRepair(content)
	if err == nil && json.Unmarshal([]byte(repaired), root) == nil {
		return true
	}
	sanitized := sanitizeJSONForRepair(content)
	repaired, err = jsonrepair.JSONRepair(sanitized)
	if err == nil && json.Unmarshal([]byte(repaired), root) == nil {
		return true
	}
	return false
}

// sanitizeJSONForRepair fixes common editing artifacts that jsonrepair
// cannot handle, such as double colons from completion glitches and
// lines with unclosed strings from mid-keystroke typing.
func sanitizeJSONForRepair(content string) string {
	// Fix double colons (e.g., "OutputName":: { → "OutputName": {).
	// The pattern ":: never appears in valid JSON, so this is safe.
	content = strings.ReplaceAll(content, "\"::", "\":")

	lines := strings.Split(content, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		quoteCount := 0
		for i := 0; i < len(trimmed); i++ {
			if trimmed[i] == '"' && (i == 0 || trimmed[i-1] != '\\') {
				quoteCount++
			}
		}
		if quoteCount%2 != 0 {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// GetResources returns the Resources section as a map.
func (pt *ParsedTemplate) GetResources() map[string]interface{} {
	if pt.Root == nil {
		return nil
	}
	resources, ok := pt.Root["Resources"].(map[string]interface{})
	if !ok {
		return nil
	}
	return resources
}

// GetResourceType returns the Type field of a named resource.
func (pt *ParsedTemplate) GetResourceType(resourceName string) string {
	resources := pt.GetResources()
	if resources == nil {
		return ""
	}
	res, ok := resources[resourceName].(map[string]interface{})
	if !ok {
		return ""
	}
	typeName, _ := res["Type"].(string)
	return typeName
}

// GetLocals returns the Locals section as a map.
func (pt *ParsedTemplate) GetLocals() map[string]interface{} {
	if pt.Root == nil {
		return nil
	}
	locals, ok := pt.Root["Locals"].(map[string]interface{})
	if !ok {
		return nil
	}
	return locals
}

// GetLocalsNames returns the names of all local variables defined in the template.
func (pt *ParsedTemplate) GetLocalsNames() []string {
	locals := pt.GetLocals()
	if locals == nil {
		return nil
	}
	names := make([]string, 0, len(locals))
	for name := range locals {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetParameters returns the Parameters section as a map.
func (pt *ParsedTemplate) GetParameters() map[string]interface{} {
	if pt.Root == nil {
		return nil
	}
	params, ok := pt.Root["Parameters"].(map[string]interface{})
	if !ok {
		return nil
	}
	return params
}

// GetParameterNames returns the names of all parameters defined in the template.
func (pt *ParsedTemplate) GetParameterNames() []string {
	if pt.Root == nil {
		return nil
	}
	params, ok := pt.Root["Parameters"].(map[string]interface{})
	if !ok {
		return nil
	}
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetResourceNames returns the logical IDs of all resources defined in the template.
func (pt *ParsedTemplate) GetResourceNames() []string {
	resources := pt.GetResources()
	if resources == nil {
		return nil
	}
	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetResourceProperties returns the Properties of a named resource.
func (pt *ParsedTemplate) GetResourceProperties(resourceName string) map[string]interface{} {
	resources := pt.GetResources()
	if resources == nil {
		return nil
	}
	res, ok := resources[resourceName].(map[string]interface{})
	if !ok {
		return nil
	}
	props, ok := res["Properties"].(map[string]interface{})
	if !ok {
		return nil
	}
	return props
}

// GetConditions returns the Conditions section as a map.
func (pt *ParsedTemplate) GetConditions() map[string]interface{} {
	if pt.Root == nil {
		return nil
	}
	conditions, ok := pt.Root["Conditions"].(map[string]interface{})
	if !ok {
		return nil
	}
	return conditions
}

// GetConditionNames returns the names of all conditions defined in the template.
func (pt *ParsedTemplate) GetConditionNames() []string {
	conditions := pt.GetConditions()
	if conditions == nil {
		return nil
	}
	names := make([]string, 0, len(conditions))
	for name := range conditions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetMappings returns the Mappings section as a map.
func (pt *ParsedTemplate) GetMappings() map[string]interface{} {
	if pt.Root == nil {
		return nil
	}
	mappings, ok := pt.Root["Mappings"].(map[string]interface{})
	if !ok {
		return nil
	}
	return mappings
}

// GetMappingNames returns the names of all maps defined in the Mappings section.
func (pt *ParsedTemplate) GetMappingNames() []string {
	mappings := pt.GetMappings()
	if mappings == nil {
		return nil
	}
	names := make([]string, 0, len(mappings))
	for name := range mappings {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetMappingFirstKeys returns the first-level keys of a named map in Mappings.
func (pt *ParsedTemplate) GetMappingFirstKeys(mapName string) []string {
	mappings := pt.GetMappings()
	if mappings == nil {
		return nil
	}
	mapData, ok := mappings[mapName].(map[string]interface{})
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(mapData))
	for k := range mapData {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// GetMappingSecondKeys returns the second-level keys for a given map and first key.
func (pt *ParsedTemplate) GetMappingSecondKeys(mapName, firstKey string) []string {
	mappings := pt.GetMappings()
	if mappings == nil {
		return nil
	}
	mapData, ok := mappings[mapName].(map[string]interface{})
	if !ok {
		return nil
	}
	firstLevel, ok := mapData[firstKey].(map[string]interface{})
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(firstLevel))
	for k := range firstLevel {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// GetAllMappingSecondKeys returns the union of all second-level keys across
// all first-level entries of a named map. Used when the first key is dynamic
// (e.g. a Ref or other intrinsic function).
func (pt *ParsedTemplate) GetAllMappingSecondKeys(mapName string) []string {
	mappings := pt.GetMappings()
	if mappings == nil {
		return nil
	}
	mapData, ok := mappings[mapName].(map[string]interface{})
	if !ok {
		return nil
	}
	seen := make(map[string]bool)
	for _, v := range mapData {
		secondLevel, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		for k := range secondLevel {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// HasROSTemplateFormatVersion checks if the template contains ROSTemplateFormatVersion.
func (pt *ParsedTemplate) HasROSTemplateFormatVersion() bool {
	if pt.Root == nil {
		return false
	}
	_, ok := pt.Root["ROSTemplateFormatVersion"]
	return ok
}

// GetROSTemplateFormatVersion returns the ROSTemplateFormatVersion value.
func (pt *ParsedTemplate) GetROSTemplateFormatVersion() string {
	if pt.Root == nil {
		return ""
	}
	v, _ := pt.Root["ROSTemplateFormatVersion"].(string)
	return v
}

// FindYAMLNodeAtPosition finds the YAML AST node at a given line and column (0-based).
func (pt *ParsedTemplate) FindYAMLNodeAtPosition(line, col int) ast.Node {
	if pt.YAMLFile == nil {
		return nil
	}
	for _, doc := range pt.YAMLFile.Docs {
		if doc.Body != nil {
			if node := findNodeAt(doc.Body, line+1, col+1); node != nil {
				return node
			}
		}
	}
	return nil
}

func findNodeAt(node ast.Node, line, col int) ast.Node {
	if node == nil {
		return nil
	}

	token := node.GetToken()
	if token != nil {
		pos := token.Position
		if pos.Line == line {
			return node
		}
	}

	switch n := node.(type) {
	case *ast.MappingNode:
		for _, v := range n.Values {
			if found := findNodeAt(v, line, col); found != nil {
				return found
			}
		}
	case *ast.MappingValueNode:
		if found := findNodeAt(n.Key, line, col); found != nil {
			return found
		}
		if found := findNodeAt(n.Value, line, col); found != nil {
			return found
		}
	case *ast.SequenceNode:
		for _, v := range n.Values {
			if found := findNodeAt(v, line, col); found != nil {
				return found
			}
		}
	}

	return nil
}

// FindKeyLineInYAML finds the line number (0-based) of a top-level key in YAML.
func FindKeyLineInYAML(content string, key string) int {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+":") || strings.HasPrefix(trimmed, key+" :") {
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				return i
			}
		}
	}
	return -1
}

// FindKeyLineInJSON finds the line number (0-based) of a top-level key in JSON.
func FindKeyLineInJSON(content string, key string) int {
	lines := strings.Split(content, "\n")
	needle := `"` + key + `"`
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, needle) {
			return i
		}
	}
	return -1
}

// FindKeyRangeInYAML returns the line range of a specific key value in content.
func FindKeyRangeInYAML(content string, key string) (startLine, startCol, endCol int) {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, key+":") || strings.HasPrefix(trimmed, key+" :") {
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				col := strings.Index(line, key)
				return i, col, col + len(key)
			}
		}
	}
	return -1, 0, 0
}

func extractMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
