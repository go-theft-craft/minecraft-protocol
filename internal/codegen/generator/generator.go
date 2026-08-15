// Package generator validates source data and renders generated data packages.
package generator

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"go/format"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/packetgen"
	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/protodef"
	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/schema"
)

// The whole directory is embedded rather than a pattern per level, so a
// version directory can be added without touching this line — and so its
// absence is not a build error before the first one exists.
//
//go:embed templates
var templateFS embed.FS

// Config selects generator input, output, package name, and stable version key.
type Config struct {
	SourceDir string
	OutDir    string
	Package   string
	Version   string
}

type templateData struct {
	Package         string
	RegistrationKey string
	TargetVersion   string
	Data            any
}

type stableVersionKey struct {
	full    string
	edition string
	target  string
}

type targetPaths struct {
	outDir string
	target string
}

type renderedFile struct {
	name string
	raw  []byte
}

var generatedFileNames = []string{
	"attributes.go",
	"biomes.go",
	"blocks.go",
	"collision_shapes.go",
	"effects.go",
	"enchantments.go",
	"entities.go",
	"foods.go",
	"gamedata.go",
	"helpers.go",
	"instruments.go",
	"items.go",
	"language.go",
	"materials.go",
	"packets.go",
	"codec.go",
	"descriptor.go",
	"particles.go",
	"physics.go",
	"protocol.go",
	"recipes.go",
	"version.go",
	"windows.go",
}

var preservedGeneratedTestNames = []string{
	"codec_test.go",
	"data_test.go",
	"roundtrip_test.go",
	"login_role_test.go",
	"transition_test.go",
}

// Run validates source data and atomically replaces the selected generated package.
func Run(config Config) (runErr error) {
	paths, files, err := prepare(config)
	if err != nil {
		return err
	}

	preservedTests, err := readPreservedGeneratedTests(paths.target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	staging, err := os.MkdirTemp(paths.outDir, "."+config.Package+".tmp-")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staging); err != nil && runErr == nil {
			runErr = fmt.Errorf("remove staging directory: %w", err)
		}
	}()

	if err := writeRenderedPackage(staging, files, preservedTests); err != nil {
		return err
	}
	if err := replaceTarget(paths.target, staging); err != nil {
		return err
	}
	return nil
}

// Check verifies the explicit generated-file inventory without changing it.
func Check(config Config) error {
	paths, files, err := prepare(config)
	if err != nil {
		return err
	}
	return compareGeneratedPackage(paths.target, files)
}

func prepare(config Config) (targetPaths, []renderedFile, error) {
	paths, versionKey, err := validateConfig(config)
	if err != nil {
		return targetPaths{}, nil, err
	}
	source, err := loadVerifiedSource(config.SourceDir)
	if err != nil {
		return targetPaths{}, nil, fmt.Errorf("validate source manifest: %w", err)
	}
	if versionKey.edition != source.Manifest.Edition {
		return targetPaths{}, nil, fmt.Errorf("version edition %q does not match manifest edition %q", versionKey.edition, source.Manifest.Edition)
	}
	if versionKey.target != source.Manifest.TargetMinecraftVersion {
		return targetPaths{}, nil, fmt.Errorf("version target %q does not match manifest target %q", versionKey.target, source.Manifest.TargetMinecraftVersion)
	}

	templates, err := newTemplateSet(templateFS, config.Package)
	if err != nil {
		return targetPaths{}, nil, err
	}
	files, err := buildRenderPlan(templates, source, config.Package, versionKey)
	if err != nil {
		return targetPaths{}, nil, err
	}
	return paths, files, nil
}

func validateConfig(config Config) (targetPaths, stableVersionKey, error) {
	if config.SourceDir == "" {
		return targetPaths{}, stableVersionKey{}, fmt.Errorf("source directory is required")
	}
	if config.OutDir == "" {
		return targetPaths{}, stableVersionKey{}, fmt.Errorf("output directory is required")
	}
	if config.Package == "" {
		return targetPaths{}, stableVersionKey{}, fmt.Errorf("package is required")
	}
	if config.Package == "_" || !token.IsIdentifier(config.Package) || token.Lookup(config.Package).IsKeyword() {
		return targetPaths{}, stableVersionKey{}, fmt.Errorf("package %q must be one non-keyword Go identifier", config.Package)
	}
	if config.Version == "" {
		return targetPaths{}, stableVersionKey{}, fmt.Errorf("version is required")
	}
	versionKey, err := parseStableVersionKey(config.Version)
	if err != nil {
		return targetPaths{}, stableVersionKey{}, err
	}
	outDir, err := filepath.Abs(config.OutDir)
	if err != nil {
		return targetPaths{}, stableVersionKey{}, fmt.Errorf("resolve output directory: %w", err)
	}
	outDir = filepath.Clean(outDir)
	target := filepath.Join(outDir, config.Package)
	relative, err := filepath.Rel(outDir, target)
	if err != nil {
		return targetPaths{}, stableVersionKey{}, fmt.Errorf("resolve generated package target: %w", err)
	}
	if relative != config.Package || filepath.Dir(target) != outDir || filepath.Base(target) != config.Package {
		return targetPaths{}, stableVersionKey{}, fmt.Errorf("generated package target %q must be a direct child of output directory %q", target, outDir)
	}
	return targetPaths{outDir: outDir, target: target}, versionKey, nil
}

func buildRenderPlan(templates templateSet, source *verifiedSource, packageName string, versionKey stableVersionKey) ([]renderedFile, error) {
	rendered := make(map[string][]byte, len(generatedFileNames))
	add := func(datasetName, templateName, outputFile string, load func([]byte) (any, error)) error {
		body, err := source.dataset(datasetName)
		if err != nil {
			return err
		}
		value, err := load(body)
		if err != nil {
			return fmt.Errorf("parse %s: %w", datasetName, err)
		}
		raw, err := renderFile(templates, templateName, newTemplateData(packageName, versionKey, value))
		if err != nil {
			return fmt.Errorf("generate %s: %w", outputFile, err)
		}
		if _, exists := rendered[outputFile]; exists {
			return fmt.Errorf("generated output %s is duplicated", outputFile)
		}
		rendered[outputFile] = raw
		return nil
	}

	arrayGenerators := []struct {
		datasetName  string
		templateName string
		outputFile   string
		load         func([]byte) (any, error)
	}{
		{"blocks", "blocks.go.tmpl", "blocks.go", loadBlocks},
		{"items", "items.go.tmpl", "items.go", loadItems},
		{"entities", "entities.go.tmpl", "entities.go", loadEntities},
		{"biomes", "biomes.go.tmpl", "biomes.go", loadBiomes},
		{"effects", "effects.go.tmpl", "effects.go", loadEffects},
		{"enchantments", "enchantments.go.tmpl", "enchantments.go", loadEnchantments},
		{"foods", "foods.go.tmpl", "foods.go", loadFoods},
		{"particles", "particles.go.tmpl", "particles.go", loadParticles},
		{"instruments", "instruments.go.tmpl", "instruments.go", loadInstruments},
		{"attributes", "attributes.go.tmpl", "attributes.go", loadAttributes},
		{"windows", "windows.go.tmpl", "windows.go", loadWindows},
	}
	for _, entry := range arrayGenerators {
		if err := add(entry.datasetName, entry.templateName, entry.outputFile, entry.load); err != nil {
			return nil, err
		}
	}

	specialGenerators := []struct {
		datasetName  string
		templateName string
		outputFile   string
		load         func([]byte) (any, error)
	}{
		{"version", "version.go.tmpl", "version.go", func(raw []byte) (any, error) { return loadVersion(raw, versionKey.target) }},
		{"language", "language.go.tmpl", "language.go", func(raw []byte) (any, error) { return loadLanguage(raw) }},
		{"materials", "materials.go.tmpl", "materials.go", func(raw []byte) (any, error) { return loadMaterials(raw) }},
		{"recipes", "recipes.go.tmpl", "recipes.go", func(raw []byte) (any, error) { return loadRecipes(raw) }},
		{"blockCollisionShapes", "collision_shapes.go.tmpl", "collision_shapes.go", func(raw []byte) (any, error) { return loadCollisionShapes(raw) }},
		{"protocol", "protocol.go.tmpl", "protocol.go", func(raw []byte) (any, error) { return loadProtocol(raw) }},
	}
	for _, entry := range specialGenerators {
		if err := add(entry.datasetName, entry.templateName, entry.outputFile, entry.load); err != nil {
			return nil, err
		}
	}

	_, hasPhysics := source.Files["physics"]
	if hasPhysics {
		if err := add("physics", "physics.go.tmpl", "physics.go", func(raw []byte) (any, error) {
			return loadPhysics(raw)
		}); err != nil {
			return nil, err
		}
	}

	for _, entry := range []struct {
		templateName string
		outputFile   string
	}{
		{"helpers.go.tmpl", "helpers.go"},
		{"gamedata.go.tmpl", "gamedata.go"},
	} {
		raw, err := renderFile(templates, entry.templateName, newTemplateData(packageName, versionKey, gamedataTmpl{HasPhysics: hasPhysics}))
		if err != nil {
			return nil, fmt.Errorf("generate %s: %w", entry.outputFile, err)
		}
		rendered[entry.outputFile] = raw
	}

	protocolSource, err := source.dataset("protocol")
	if err != nil {
		return nil, err
	}
	packetSchema, err := protodef.Parse(protocolSource)
	if err != nil {
		return nil, fmt.Errorf("parse the protocol dataset for packet generation: %w", err)
	}
	packetModel, err := packetgen.Build(framedPacketSchema(packetSchema), packetgen.Options{PackageName: packageName})
	if err != nil {
		return nil, fmt.Errorf("build packet model: %w", err)
	}
	packetFiles, err := packetgen.Generate(packetModel, packetgen.Options{PackageName: packageName})
	if err != nil {
		return nil, fmt.Errorf("generate packet codecs: %w", err)
	}
	packetFiles["descriptor.go"], err = addPacketValueKeys(packetFiles["descriptor.go"], packetModel)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"packets.go", "codec.go", "descriptor.go"} {
		raw, ok := packetFiles[name]
		if !ok {
			return nil, fmt.Errorf("packet generation is missing %s", name)
		}
		if _, exists := rendered[name]; exists {
			return nil, fmt.Errorf("generated output %s is duplicated", name)
		}
		rendered[name] = raw
		delete(packetFiles, name)
	}
	if len(packetFiles) != 0 {
		return nil, fmt.Errorf("packet generation contains %d unexpected files", len(packetFiles))
	}

	files := make([]renderedFile, 0, len(generatedFileNames))
	for _, name := range generatedFileNames {
		raw, ok := rendered[name]
		if !ok {
			if optionalGeneratedFileNames[name] {
				continue
			}

			return nil, fmt.Errorf("render plan is missing %s", name)
		}
		files = append(files, renderedFile{name: name, raw: raw})
		delete(rendered, name)
	}
	if len(rendered) != 0 {
		return nil, fmt.Errorf("render plan contains %d unexpected files", len(rendered))
	}
	return files, nil
}

func framedPacketSchema(schema *protodef.Schema) *protodef.Schema {
	framed := *schema
	framed.States = slices.Clone(schema.States)
	for stateIndex := range framed.States {
		state := &framed.States[stateIndex]
		state.Directions = slices.Clone(state.Directions)
		for directionIndex := range state.Directions {
			direction := &state.Directions[directionIndex]
			direction.Packets = slices.DeleteFunc(slices.Clone(direction.Packets), func(packet protodef.Packet) bool {
				return !isFramedPacket(state.Name, direction.Name, packet.Name, packet.ID)
			})
		}
	}
	return &framed
}

func isFramedPacket(state, direction, name string, id int) bool {
	return state != "handshaking" || direction != "toServer" || name != "legacy_server_list_ping" || id != 0xfe
}

// loginRoleFor reports the part a packet plays in a login sequence, as the
// unqualified name of a protocol.LoginRole constant, or "" for a packet with
// no part.
//
// The mapping lives here rather than in the schema because the schema
// describes wire layout and says nothing about sequence. It is keyed by
// upstream state, direction, and packet name, so a later protocol whose
// packets keep these names inherits the tagging, and one whose packets do not
// simply reports no role until this function learns its names.
func loginRoleFor(state, direction, name string) string {
	if state != "login" {
		return ""
	}

	switch direction {
	case "toClient":
		switch name {
		case "encryption_begin":
			return "RoleEncryptionRequest"
		case "success":
			return "RoleLoginSuccess"
		case "compress":
			return "RoleSetCompression"
		}
	case "toServer":
		switch name {
		case "login_start":
			return "RoleLoginStart"
		case "encryption_begin":
			return "RoleEncryptionResponse"
		}
	}

	return ""
}

func addPacketValueKeys(descriptor []byte, model *packetgen.Model) ([]byte, error) {
	if len(descriptor) == 0 {
		return nil, fmt.Errorf("packet generation returned an empty descriptor.go")
	}

	var source bytes.Buffer
	source.Write(descriptor)
	source.WriteString("\nfunc packetKeyForValue(value packetCodec) (packetKey, bool) {\n")
	source.WriteString("\tswitch value.(type) {\n")
	for _, factory := range model.Factories {
		_, _ = fmt.Fprintf(
			&source,
			"\tcase *%s:\n\t\treturn packetKey{State: protocol.State(%s), Direction: %s, ID: %d}, true\n",
			factory.PacketType,
			strconv.Quote(factory.State),
			factory.DirectionValue,
			factory.ID,
		)
	}
	source.WriteString("\tdefault:\n\t\treturn packetKey{}, false\n\t}\n}\n")

	formatted, err := format.Source(source.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated packet value registry: %w", err)
	}
	return formatted, nil
}

func parseStableVersionKey(value string) (stableVersionKey, error) {
	edition, target, found := strings.Cut(value, "/")
	if !found || edition == "" || target == "" || strings.Contains(target, "/") {
		return stableVersionKey{}, fmt.Errorf("version %q must be a stable key in edition/target form", value)
	}
	return stableVersionKey{full: value, edition: edition, target: target}, nil
}

// optionalGeneratedFileNames are generated only for source trees that carry the
// dataset behind them. Physics constants are measured from a Mojang jar, and
// only the versions someone has run mcreference against have them.
var optionalGeneratedFileNames = map[string]bool{
	"physics.go": true,
}

func newTemplateData(packageName string, versionKey stableVersionKey, value any) templateData {
	return templateData{
		Package:         packageName,
		RegistrationKey: versionKey.full,
		TargetVersion:   versionKey.target,
		Data:            value,
	}
}

func readPreservedGeneratedTests(target string) (map[string][]byte, error) {
	preserved := make(map[string][]byte, len(preservedGeneratedTestNames))
	for _, name := range preservedGeneratedTestNames {
		raw, err := os.ReadFile(filepath.Join(target, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read preserved %s: %w", name, err)
		}
		preserved[name] = raw
	}
	return preserved, nil
}

func writeRenderedPackage(staging string, files []renderedFile, preservedTests map[string][]byte) error {
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(staging, file.name), file.raw, 0o644); err != nil {
			return fmt.Errorf("write staged %s: %w", file.name, err)
		}
	}
	for _, name := range preservedGeneratedTestNames {
		raw, ok := preservedTests[name]
		if !ok {
			continue
		}
		if err := os.WriteFile(filepath.Join(staging, name), raw, 0o644); err != nil {
			return fmt.Errorf("write staged %s: %w", name, err)
		}
	}
	return nil
}

func replaceTarget(target, staging string) error {
	_, err := os.Stat(target)
	if os.IsNotExist(err) {
		if err := os.Rename(staging, target); err != nil {
			return fmt.Errorf("install generated package: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect generated package target: %w", err)
	}

	backup := staging + ".backup"
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("move previous generated package to backup: %w", err)
	}
	if err := os.Rename(staging, target); err != nil {
		replaceErr := err
		if rollbackErr := os.Rename(backup, target); rollbackErr != nil {
			return fmt.Errorf("install generated package: %v; restore previous package: %w", replaceErr, rollbackErr)
		}
		return fmt.Errorf("install generated package: %w", replaceErr)
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove generated package backup: %w", err)
	}
	return nil
}

func compareGeneratedPackage(target string, files []renderedFile) error {
	entries, err := os.ReadDir(target)
	if err != nil {
		return fmt.Errorf("read generated output: %w", err)
	}
	expected := make(map[string][]byte, len(files))
	for _, file := range files {
		expected[file.name] = file.raw
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if slices.Contains(preservedGeneratedTestNames, name) {
			if entry.Type().IsRegular() {
				continue
			}
			return fmt.Errorf("generated output has extra file %s", name)
		}
		want, ok := expected[name]
		if !ok || !entry.Type().IsRegular() {
			return fmt.Errorf("generated output has extra file %s", name)
		}
		seen[name] = true
		got, err := os.ReadFile(filepath.Join(target, name))
		if err != nil {
			return fmt.Errorf("read generated output %s: %w", name, err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("generated output changed %s", name)
		}
	}
	for _, name := range generatedFileNames {
		if !seen[name] && !optionalGeneratedFileNames[name] {
			return fmt.Errorf("generated output is missing %s", name)
		}
	}
	return nil
}

func renderFile(templates templateSet, name string, value any) ([]byte, error) {
	return templates.render(name, value)
}

func renderTemplate(defined *template.Template, name string, value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := defined.Execute(&buffer, value); err != nil {
		return nil, fmt.Errorf("execute template %s: %w", name, err)
	}
	formatted, err := format.Source(buffer.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format template %s: %w", name, err)
	}

	return formatted, nil
}

type blockTmpl struct {
	schema.Block
	Drops        []dropTmpl
	HarvestTools map[int]bool
}

type dropTmpl struct {
	ID          int
	Metadata    int
	MinCount    float64
	MaxCount    float64
	HasMinCount bool
	HasMaxCount bool
}

func loadBlocks(raw []byte) (any, error) {
	blocks, err := schema.LoadJSON[schema.Block](raw)
	if err != nil {
		return nil, err
	}

	result := make([]blockTmpl, len(blocks))
	for index, block := range blocks {
		drops := make([]dropTmpl, len(block.Drops))
		for dropIndex, drop := range block.Drops {
			id, metadata, err := drop.Parse()
			if err != nil {
				return nil, fmt.Errorf("block %d drop %d: %w", block.ID, dropIndex, err)
			}
			parsed := dropTmpl{ID: id, Metadata: metadata, HasMinCount: drop.MinCount != nil, HasMaxCount: drop.MaxCount != nil}
			if drop.MinCount != nil {
				parsed.MinCount = *drop.MinCount
			}
			if drop.MaxCount != nil {
				parsed.MaxCount = *drop.MaxCount
			}
			drops[dropIndex] = parsed
		}
		harvestTools := make(map[int]bool, len(block.HarvestTools))
		for rawID, harvestable := range block.HarvestTools {
			id, err := strconv.Atoi(rawID)
			if err != nil {
				return nil, fmt.Errorf("block %d harvest tool %q: %w", block.ID, rawID, err)
			}
			harvestTools[id] = harvestable
		}
		result[index] = blockTmpl{Block: block, Drops: drops, HarvestTools: harvestTools}
	}
	return result, nil
}

func loadItems(raw []byte) (any, error) { return schema.LoadJSON[schema.Item](raw) }

type entityTmpl struct {
	schema.Entity
	EntityType string
}

func loadEntities(raw []byte) (any, error) {
	entities, err := schema.LoadJSON[schema.Entity](raw)
	if err != nil {
		return nil, err
	}
	result := make([]entityTmpl, len(entities))
	seen := make(map[string]struct{}, len(entities))
	for index, entity := range entities {
		entityType, known := entityTypeConstants[entity.Type]
		if !known {
			return nil, fmt.Errorf("entity %d has unsupported type %q", entity.ID, entity.Type)
		}
		key := fmt.Sprintf("%s/%d", entity.Type, entity.ID)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate entity typed ID %s", key)
		}
		seen[key] = struct{}{}
		result[index] = entityTmpl{Entity: entity, EntityType: entityType}
	}
	return result, nil
}
func loadBiomes(raw []byte) (any, error)       { return schema.LoadJSON[schema.Biome](raw) }
func loadEffects(raw []byte) (any, error)      { return schema.LoadJSON[schema.Effect](raw) }
func loadEnchantments(raw []byte) (any, error) { return schema.LoadJSON[schema.Enchantment](raw) }
func loadFoods(raw []byte) (any, error)        { return schema.LoadJSON[schema.Food](raw) }
func loadParticles(raw []byte) (any, error)    { return schema.LoadJSON[schema.Particle](raw) }
func loadInstruments(raw []byte) (any, error)  { return schema.LoadJSON[schema.Instrument](raw) }
func loadAttributes(raw []byte) (any, error)   { return schema.LoadJSON[schema.Attribute](raw) }
func loadWindows(raw []byte) (any, error)      { return schema.LoadJSON[schema.Window](raw) }

type versionTmpl struct {
	Protocol         int
	MinecraftVersion string
	MajorVersion     string
	TargetVersion    string
	MetadataEnd      byte
}

func loadVersion(raw []byte, targetVersion string) (*versionTmpl, error) {
	var version schema.VersionInfo
	if err := json.Unmarshal(raw, &version); err != nil {
		return nil, fmt.Errorf("unmarshal version: %w", err)
	}
	return &versionTmpl{
		Protocol:         version.Version,
		MinecraftVersion: version.MinecraftVersion,
		MajorVersion:     version.MajorVersion,
		TargetVersion:    targetVersion,
		MetadataEnd:      0x7F,
	}, nil
}

func loadLanguage(raw []byte) (map[string]string, error) {
	var language map[string]string
	if err := json.Unmarshal(raw, &language); err != nil {
		return nil, fmt.Errorf("unmarshal language: %w", err)
	}
	return language, nil
}

type materialTmpl struct {
	Name       string
	ToolSpeeds map[int]float64
}

func loadMaterials(raw []byte) ([]materialTmpl, error) {
	var rawMaterials map[string]map[string]float64
	if err := json.Unmarshal(raw, &rawMaterials); err != nil {
		return nil, fmt.Errorf("unmarshal materials: %w", err)
	}
	result := make([]materialTmpl, 0, len(rawMaterials))
	for name, tools := range rawMaterials {
		toolSpeeds := make(map[int]float64, len(tools))
		for rawID, speed := range tools {
			id, err := strconv.Atoi(rawID)
			if err != nil {
				return nil, fmt.Errorf("material %s tool %q: %w", name, rawID, err)
			}
			toolSpeeds[id] = speed
		}
		result = append(result, materialTmpl{Name: name, ToolSpeeds: toolSpeeds})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

type recipeTmplEntry struct {
	ID      int
	Recipes []recipeTmpl
}

type recipeTmpl struct {
	Ingredients []ingredientTmpl
	InShape     [][]ingredientTmpl
	OutShape    [][]*ingredientTmpl
	Result      schema.RecipeResult
}

type ingredientTmpl struct {
	ID       int
	Metadata int
}

func loadRecipes(raw []byte) ([]recipeTmplEntry, error) {
	var rawRecipes map[string][]schema.RawRecipe
	if err := json.Unmarshal(raw, &rawRecipes); err != nil {
		return nil, fmt.Errorf("unmarshal recipes: %w", err)
	}
	result := make([]recipeTmplEntry, 0, len(rawRecipes))
	for rawID, recipes := range rawRecipes {
		id, err := strconv.Atoi(rawID)
		if err != nil {
			return nil, fmt.Errorf("recipe result ID %q: %w", rawID, err)
		}
		parsedRecipes := make([]recipeTmpl, len(recipes))
		for index, recipe := range recipes {
			ingredients := make([]ingredientTmpl, len(recipe.Ingredients))
			for ingredientIndex, rawIngredient := range recipe.Ingredients {
				parsed := schema.ParseIngredient(rawIngredient)
				ingredients[ingredientIndex] = ingredientTmpl{ID: parsed.ID, Metadata: fixLogMeta(parsed)}
			}
			shape := make([][]ingredientTmpl, len(recipe.InShape))
			for rowIndex, row := range recipe.InShape {
				shape[rowIndex] = make([]ingredientTmpl, len(row))
				for columnIndex, rawIngredient := range row {
					parsed := schema.ParseIngredient(rawIngredient)
					shape[rowIndex][columnIndex] = ingredientTmpl{ID: parsed.ID, Metadata: fixLogMeta(parsed)}
				}
			}
			outShape := make([][]*ingredientTmpl, len(recipe.OutShape))
			for rowIndex, row := range recipe.OutShape {
				outShape[rowIndex] = make([]*ingredientTmpl, len(row))
				for columnIndex, rawIngredient := range row {
					if bytes.Equal(bytes.TrimSpace(rawIngredient), []byte("null")) {
						continue
					}
					parsed := schema.ParseIngredient(rawIngredient)
					ingredient := ingredientTmpl{ID: parsed.ID, Metadata: fixLogMeta(parsed)}
					outShape[rowIndex][columnIndex] = &ingredient
				}
			}
			parsedRecipes[index] = recipeTmpl{Ingredients: ingredients, InShape: shape, OutShape: outShape, Result: recipe.Result}
		}
		result = append(result, recipeTmplEntry{ID: id, Recipes: parsedRecipes})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func fixLogMeta(ingredient schema.RecipeIngredient) int {
	if (ingredient.ID == 17 || ingredient.ID == 162) && ingredient.Metadata >= 4 {
		return ingredient.Metadata & 0x3
	}
	return ingredient.Metadata
}

// gamedataTmpl carries the render-time facts gamedata.go.tmpl branches on.
type gamedataTmpl struct {
	HasPhysics bool
}

type physicsTmpl struct {
	DefaultSlipperiness float64
	SinTableBase64      string
	BlockSlipperiness   []slipperinessTmpl
	EntityMotion        []entityMotionTmpl
}

type slipperinessTmpl struct {
	Name  string
	Value float64
}

type entityMotionTmpl struct {
	Name           string
	Gravity        float64
	HorizontalDrag float64
	VerticalDrag   float64
	StepHeight     float64
}

func loadPhysics(raw []byte) (*physicsTmpl, error) {
	var source struct {
		Version             string             `json:"version"`
		Side                string             `json:"side"`
		JarSHA256           string             `json:"jarSha256"`
		DefaultSlipperiness float64            `json:"defaultSlipperiness"`
		BlockSlipperiness   map[string]float64 `json:"blockSlipperiness"`
		SinTableBase64      string             `json:"sinTableBase64"`
		EntityMotion        map[string]struct {
			Gravity        float64 `json:"gravity"`
			HorizontalDrag float64 `json:"horizontalDrag"`
			VerticalDrag   float64 `json:"verticalDrag"`
			StepHeight     float64 `json:"stepHeight"`
		} `json:"entityMotion"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("unmarshal physics: %w", err)
	}
	if source.SinTableBase64 == "" {
		return nil, fmt.Errorf("physics is missing the sin table")
	}
	if len(source.BlockSlipperiness) == 0 {
		return nil, fmt.Errorf("physics is missing block slipperiness")
	}
	if len(source.EntityMotion) == 0 {
		return nil, fmt.Errorf("physics is missing entity motion constants")
	}

	blocks := make([]slipperinessTmpl, 0, len(source.BlockSlipperiness))
	for name, value := range source.BlockSlipperiness {
		blocks = append(blocks, slipperinessTmpl{Name: name, Value: value})
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Name < blocks[j].Name })

	motion := make([]entityMotionTmpl, 0, len(source.EntityMotion))
	for name, value := range source.EntityMotion {
		motion = append(motion, entityMotionTmpl{
			Name:           name,
			Gravity:        value.Gravity,
			HorizontalDrag: value.HorizontalDrag,
			VerticalDrag:   value.VerticalDrag,
			StepHeight:     value.StepHeight,
		})
	}
	sort.Slice(motion, func(i, j int) bool { return motion[i].Name < motion[j].Name })

	return &physicsTmpl{
		DefaultSlipperiness: source.DefaultSlipperiness,
		SinTableBase64:      source.SinTableBase64,
		BlockSlipperiness:   blocks,
		EntityMotion:        motion,
	}, nil
}

type collisionShapesTmpl struct {
	Blocks []collisionBlockTmpl
	Shapes []collisionShapeTmpl
}

type collisionBlockTmpl struct {
	Name     string
	ShapeIDs []int
}

type collisionShapeTmpl struct {
	ID    int
	Boxes [][]float64
}

func loadCollisionShapes(raw []byte) (*collisionShapesTmpl, error) {
	var source schema.RawCollisionShapes
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("unmarshal collision shapes: %w", err)
	}
	blocks := make([]collisionBlockTmpl, 0, len(source.Blocks))
	for name, rawShapeIDs := range source.Blocks {
		var shapeIDs []int
		var singleID int
		if err := json.Unmarshal(rawShapeIDs, &singleID); err == nil {
			shapeIDs = []int{singleID}
		} else if err := json.Unmarshal(rawShapeIDs, &shapeIDs); err != nil {
			return nil, fmt.Errorf("parse block shapes for %s: %w", name, err)
		}
		blocks = append(blocks, collisionBlockTmpl{Name: name, ShapeIDs: shapeIDs})
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].Name < blocks[j].Name })

	shapes := make([]collisionShapeTmpl, 0, len(source.Shapes))
	for rawID, boxes := range source.Shapes {
		id, err := strconv.Atoi(rawID)
		if err != nil {
			return nil, fmt.Errorf("collision shape ID %q: %w", rawID, err)
		}
		shapes = append(shapes, collisionShapeTmpl{ID: id, Boxes: boxes})
	}
	sort.Slice(shapes, func(i, j int) bool { return shapes[i].ID < shapes[j].ID })
	return &collisionShapesTmpl{Blocks: blocks, Shapes: shapes}, nil
}

type protocolTmpl struct {
	Types  map[string]string
	Phases []protocolPhaseTmpl
}

type protocolPhaseTmpl struct {
	Name     string
	ToClient []packetTmpl
	ToServer []packetTmpl
}

type packetTmpl struct {
	Name   string
	ID     int
	Fields []packetFieldTmpl
	Framed bool
	// LoginRole is the unqualified name of the protocol.LoginRole constant
	// this packet plays, or "" when it plays no part in a login.
	LoginRole string
}

type packetFieldTmpl struct {
	Name string
	Type string
}

func loadProtocol(raw []byte) (*protocolTmpl, error) {
	var rawProtocol map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawProtocol); err != nil {
		return nil, fmt.Errorf("unmarshal protocol: %w", err)
	}
	if rawProtocol == nil {
		return nil, fmt.Errorf("protocol root must be an object")
	}
	types := make(map[string]string)
	typesRaw, ok := rawProtocol["types"]
	if !ok {
		return nil, fmt.Errorf("protocol types are missing")
	}
	var rawTypes map[string]json.RawMessage
	if err := json.Unmarshal(typesRaw, &rawTypes); err != nil {
		return nil, fmt.Errorf("unmarshal protocol types: %w", err)
	}
	if rawTypes == nil {
		return nil, fmt.Errorf("protocol types must be an object")
	}
	for name, value := range rawTypes {
		if name == "" {
			return nil, fmt.Errorf("protocol type has an empty name")
		}
		summary, err := summarizeProtocolType(value, "protocol type "+name)
		if err != nil {
			return nil, err
		}
		types[name] = summary
	}

	phases := make([]protocolPhaseTmpl, 0, 4)
	for _, phaseName := range []string{"handshaking", "status", "login", "play"} {
		phaseRaw, ok := rawProtocol[phaseName]
		if !ok {
			return nil, fmt.Errorf("protocol phase %s is missing", phaseName)
		}
		var phase struct {
			ToClient *struct {
				Types map[string]json.RawMessage `json:"types"`
			} `json:"toClient"`
			ToServer *struct {
				Types map[string]json.RawMessage `json:"types"`
			} `json:"toServer"`
		}
		if err := json.Unmarshal(phaseRaw, &phase); err != nil {
			return nil, fmt.Errorf("unmarshal phase %s: %w", phaseName, err)
		}
		if phase.ToClient == nil {
			return nil, fmt.Errorf("protocol phase %s direction toClient is missing", phaseName)
		}
		if phase.ToServer == nil {
			return nil, fmt.Errorf("protocol phase %s direction toServer is missing", phaseName)
		}
		toClient, err := extractPackets(phase.ToClient.Types, phaseName, "toClient")
		if err != nil {
			return nil, err
		}
		toServer, err := extractPackets(phase.ToServer.Types, phaseName, "toServer")
		if err != nil {
			return nil, err
		}
		phases = append(phases, protocolPhaseTmpl{Name: phaseName, ToClient: toClient, ToServer: toServer})
	}
	return &protocolTmpl{Types: types, Phases: phases}, nil
}

func summarizeProtocolType(raw json.RawMessage, location string) (string, error) {
	var native string
	if err := json.Unmarshal(raw, &native); err == nil {
		if native == "" {
			return "", fmt.Errorf("%s is empty", location)
		}
		return native, nil
	}
	if _, _, err := parseProtocolDefinition(raw, location); err != nil {
		return "", err
	}
	return "complex", nil
}

func parseProtocolDefinition(raw json.RawMessage, location string) (string, json.RawMessage, error) {
	var definition []json.RawMessage
	if err := json.Unmarshal(raw, &definition); err != nil {
		return "", nil, fmt.Errorf("%s must be a named complex definition: %w", location, err)
	}
	if len(definition) != 2 {
		return "", nil, fmt.Errorf("%s must contain a type name and options", location)
	}
	var kind string
	if err := json.Unmarshal(definition[0], &kind); err != nil || kind == "" {
		return "", nil, fmt.Errorf("%s has an invalid type name", location)
	}
	if len(bytes.TrimSpace(definition[1])) == 0 || bytes.Equal(bytes.TrimSpace(definition[1]), []byte("null")) {
		return "", nil, fmt.Errorf("%s has missing options", location)
	}
	var options any
	if err := json.Unmarshal(definition[1], &options); err != nil {
		return "", nil, fmt.Errorf("%s has invalid options: %w", location, err)
	}
	switch options.(type) {
	case string, []any, map[string]any:
	default:
		return "", nil, fmt.Errorf("%s options must be a type name, object, or array", location)
	}
	return kind, definition[1], nil
}

type protocolFieldDefinition struct {
	Name string          `json:"name"`
	Type json.RawMessage `json:"type"`
}

func extractPackets(types map[string]json.RawMessage, phaseName, direction string) ([]packetTmpl, error) {
	location := fmt.Sprintf("protocol phase %s direction %s", phaseName, direction)
	if types == nil {
		return nil, fmt.Errorf("%s types are missing", location)
	}
	packetRaw, ok := types["packet"]
	if !ok {
		return nil, fmt.Errorf("%s packet definition is missing", location)
	}
	kind, fieldsRaw, err := parseProtocolDefinition(packetRaw, location+" packet")
	if err != nil {
		return nil, err
	}
	if kind != "container" {
		return nil, fmt.Errorf("%s packet definition type is %q, want container", location, kind)
	}
	var fields []protocolFieldDefinition
	if err := json.Unmarshal(fieldsRaw, &fields); err != nil {
		return nil, fmt.Errorf("%s packet fields: %w", location, err)
	}
	var nameType, paramsType json.RawMessage
	seenRootFields := make(map[string]bool, len(fields))
	for _, field := range fields {
		if field.Name == "" {
			return nil, fmt.Errorf("%s packet has a field with an empty name", location)
		}
		if seenRootFields[field.Name] {
			return nil, fmt.Errorf("%s packet has duplicate field %q", location, field.Name)
		}
		seenRootFields[field.Name] = true
		switch field.Name {
		case "name":
			nameType = field.Type
		case "params":
			paramsType = field.Type
		}
	}
	if len(nameType) == 0 || len(paramsType) == 0 {
		return nil, fmt.Errorf("%s packet must define name and params fields", location)
	}

	mapperKind, mapperRaw, err := parseProtocolDefinition(nameType, location+" packet name mapper")
	if err != nil {
		return nil, err
	}
	if mapperKind != "mapper" {
		return nil, fmt.Errorf("%s packet name type is %q, want mapper", location, mapperKind)
	}
	var mapper struct {
		Type     string             `json:"type"`
		Mappings *map[string]string `json:"mappings"`
	}
	if err := json.Unmarshal(mapperRaw, &mapper); err != nil {
		return nil, fmt.Errorf("%s packet name mapper: %w", location, err)
	}
	if mapper.Type != "varint" || mapper.Mappings == nil {
		return nil, fmt.Errorf("%s packet name mapper must use varint and define mappings", location)
	}

	switchKind, switchRaw, err := parseProtocolDefinition(paramsType, location+" packet params switch")
	if err != nil {
		return nil, err
	}
	if switchKind != "switch" {
		return nil, fmt.Errorf("%s packet params type is %q, want switch", location, switchKind)
	}
	var packetSwitch struct {
		CompareTo string             `json:"compareTo"`
		Fields    *map[string]string `json:"fields"`
	}
	if err := json.Unmarshal(switchRaw, &packetSwitch); err != nil {
		return nil, fmt.Errorf("%s packet params switch: %w", location, err)
	}
	if packetSwitch.CompareTo != "name" || packetSwitch.Fields == nil {
		return nil, fmt.Errorf("%s packet params switch must compare to name and define fields", location)
	}

	mappings := make(map[string]int, len(*mapper.Mappings))
	ids := make(map[int]string, len(*mapper.Mappings))
	for rawID, name := range *mapper.Mappings {
		if name == "" {
			return nil, fmt.Errorf("%s packet mapping %q has an empty name", location, rawID)
		}
		id64, err := strconv.ParseInt(rawID, 0, 64)
		if err != nil || id64 < 0 || id64 > math.MaxInt32 {
			return nil, fmt.Errorf("%s packet mapping %q has an invalid ID", location, rawID)
		}
		id := int(id64)
		if previous, exists := mappings[name]; exists {
			return nil, fmt.Errorf("%s packet name %q has duplicate IDs %d and %d", location, name, previous, id)
		}
		if previous, exists := ids[id]; exists {
			return nil, fmt.Errorf("%s packet ID %d is shared by %q and %q", location, id, previous, name)
		}
		mappings[name] = id
		ids[id] = name
		wantType := "packet_" + name
		if gotType, ok := (*packetSwitch.Fields)[name]; !ok || gotType != wantType {
			return nil, fmt.Errorf("%s packet mapping %q is missing matching switch field %q", location, name, wantType)
		}
	}
	for name, typeName := range *packetSwitch.Fields {
		if _, ok := mappings[name]; !ok {
			return nil, fmt.Errorf("%s packet switch field %q has no mapping", location, name)
		}
		if typeName != "packet_"+name {
			return nil, fmt.Errorf("%s packet switch field %q references %q", location, name, typeName)
		}
	}

	packets := make([]packetTmpl, 0, len(types))
	for typeName, typeRaw := range types {
		if !strings.HasPrefix(typeName, "packet_") || typeName == "packet_" {
			continue
		}
		name := strings.TrimPrefix(typeName, "packet_")
		id, ok := mappings[name]
		if !ok {
			return nil, fmt.Errorf("%s packet type %q has no mapping", location, typeName)
		}
		packetFields, err := extractPacketFields(typeRaw, location+" packet "+name)
		if err != nil {
			return nil, err
		}
		packets = append(packets, packetTmpl{
			Name:      name,
			ID:        id,
			Fields:    packetFields,
			Framed:    isFramedPacket(phaseName, direction, name, id),
			LoginRole: loginRoleFor(phaseName, direction, name),
		})
	}
	for name := range mappings {
		if _, ok := types["packet_"+name]; !ok {
			return nil, fmt.Errorf("%s packet mapping %q has no packet definition", location, name)
		}
	}
	sort.Slice(packets, func(i, j int) bool {
		if packets[i].ID != packets[j].ID {
			return packets[i].ID < packets[j].ID
		}
		return packets[i].Name < packets[j].Name
	})
	return packets, nil
}

func extractPacketFields(raw json.RawMessage, location string) ([]packetFieldTmpl, error) {
	kind, fieldsRaw, err := parseProtocolDefinition(raw, location)
	if err != nil {
		return nil, err
	}
	if kind != "container" {
		return nil, fmt.Errorf("%s type is %q, want container", location, kind)
	}
	var fields []protocolFieldDefinition
	if err := json.Unmarshal(fieldsRaw, &fields); err != nil {
		return nil, fmt.Errorf("%s fields: %w", location, err)
	}
	result := make([]packetFieldTmpl, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		if field.Name == "" {
			return nil, fmt.Errorf("%s has a field with an empty name", location)
		}
		if seen[field.Name] {
			return nil, fmt.Errorf("%s has duplicate field %q", location, field.Name)
		}
		seen[field.Name] = true
		if len(bytes.TrimSpace(field.Type)) == 0 || bytes.Equal(bytes.TrimSpace(field.Type), []byte("null")) {
			return nil, fmt.Errorf("%s field %q has no type", location, field.Name)
		}
		typeName, err := summarizeProtocolType(field.Type, location+" field "+field.Name)
		if err != nil {
			return nil, err
		}
		if typeName == "complex" && isBufferVarInt(field.Type) {
			typeName = "ByteArray"
		}
		result = append(result, packetFieldTmpl{Name: field.Name, Type: typeName})
	}
	return result, nil
}

func isBufferVarInt(raw json.RawMessage) bool {
	var definition []json.RawMessage
	if err := json.Unmarshal(raw, &definition); err != nil || len(definition) != 2 {
		return false
	}
	var typeName string
	if err := json.Unmarshal(definition[0], &typeName); err != nil || typeName != "buffer" {
		return false
	}
	var options struct {
		CountType string `json:"countType"`
	}
	return json.Unmarshal(definition[1], &options) == nil && options.CountType == "varint"
}

type packetStructsTmpl struct {
	Packets []packetStructDef
}

type packetStructDef struct {
	StructName string
	PacketID   int
	Fields     []packetStructFieldDef
}

type packetStructFieldDef struct {
	GoName string
	GoType string
	McTag  string
}

type typeMapping struct {
	goType string
	mcTag  string
}

var marshalableTypes = map[string]typeMapping{
	"varint":     {"int32", "varint"},
	"varlong":    {"int64", "varlong"},
	"i8":         {"int8", "i8"},
	"u8":         {"uint8", "u8"},
	"i16":        {"int16", "i16"},
	"u16":        {"uint16", "u16"},
	"i32":        {"int32", "i32"},
	"i64":        {"int64", "i64"},
	"f32":        {"float32", "f32"},
	"f64":        {"float64", "f64"},
	"bool":       {"bool", "bool"},
	"string":     {"string", "string"},
	"UUID":       {"[16]byte", "uuid"},
	"position":   {"int64", "position"},
	"ByteArray":  {"[]byte", "bytearray"},
	"restBuffer": {"[]byte", "rest"},
}

func loadPacketStructs(raw []byte) (*packetStructsTmpl, error) {
	protocol, err := loadProtocol(raw)
	if err != nil {
		return nil, err
	}
	var packets []packetStructDef
	structNames := make(map[string]bool)
	appendPacket := func(packet packetTmpl, suffix string) error {
		if packet.Name == "legacy_server_list_ping" {
			return nil
		}
		definition := buildPacketStructDef(packet, suffix)
		if structNames[definition.StructName] {
			return fmt.Errorf("packet struct name %s is duplicated", definition.StructName)
		}
		structNames[definition.StructName] = true
		packets = append(packets, definition)
		return nil
	}
	for _, phase := range protocol.Phases {
		clientNames := make(map[string]bool, len(phase.ToClient))
		serverNames := make(map[string]bool, len(phase.ToServer))
		for _, packet := range phase.ToClient {
			clientNames[packet.Name] = true
		}
		for _, packet := range phase.ToServer {
			serverNames[packet.Name] = true
		}
		for _, packet := range phase.ToClient {
			suffix := ""
			if serverNames[packet.Name] {
				suffix = "CB"
			}
			if err := appendPacket(packet, suffix); err != nil {
				return nil, err
			}
		}
		for _, packet := range phase.ToServer {
			suffix := ""
			if clientNames[packet.Name] {
				suffix = "SB"
			}
			if err := appendPacket(packet, suffix); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(packets, func(i, j int) bool { return packets[i].StructName < packets[j].StructName })
	return &packetStructsTmpl{Packets: packets}, nil
}

func buildPacketStructDef(packet packetTmpl, suffix string) packetStructDef {
	fields := make([]packetStructFieldDef, 0, len(packet.Fields))
	for _, field := range packet.Fields {
		mapping, ok := marshalableTypes[field.Type]
		if !ok {
			fields = []packetStructFieldDef{{GoName: "Data", GoType: "[]byte", McTag: "rest"}}
			return packetStructDef{StructName: snakeToPascal(packet.Name) + suffix, PacketID: packet.ID, Fields: fields}
		}
		fields = append(fields, packetStructFieldDef{GoName: camelToPascal(field.Name), GoType: mapping.goType, McTag: mapping.mcTag})
	}
	return packetStructDef{StructName: snakeToPascal(packet.Name) + suffix, PacketID: packet.ID, Fields: fields}
}

func snakeToPascal(value string) string {
	parts := strings.Split(value, "_")
	for index, part := range parts {
		if part != "" {
			parts[index] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return fixAbbreviations(strings.Join(parts, ""))
}

func camelToPascal(value string) string {
	if value == "" {
		return ""
	}
	return fixAbbreviations(strings.ToUpper(value[:1]) + value[1:])
}

func fixAbbreviations(value string) string {
	value = strings.ReplaceAll(value, "Uuid", "UUID")
	value = strings.ReplaceAll(value, "Nbt", "NBT")
	value = strings.ReplaceAll(value, "Url", "URL")
	var result strings.Builder
	for index := 0; index < len(value); index++ {
		if index+1 < len(value) && value[index:index+2] == "Id" {
			atEnd := index+2 >= len(value)
			beforeUpper := !atEnd && value[index+2] >= 'A' && value[index+2] <= 'Z'
			if atEnd || beforeUpper {
				result.WriteString("ID")
				index++
				continue
			}
		}
		result.WriteByte(value[index])
	}
	return result.String()
}

// entityTypeConstants maps upstream's entity classification to the Go constant
// that names it. It is a closed set on purpose: a classification nobody has
// seen fails generation rather than being carried through as free text.
//
// Java 1.8 has two values, which name the ID namespace an entity came from.
// Java 26.1 replaced that with a classification of the entity itself and has
// ten. Both are listed here, because one generator serves both.
var entityTypeConstants = map[string]string{
	"mob":            "data.EntityTypeMob",
	"object":         "data.EntityTypeObject",
	"ambient":        "data.EntityTypeAmbient",
	"animal":         "data.EntityTypeAnimal",
	"hostile":        "data.EntityTypeHostile",
	"living":         "data.EntityTypeLiving",
	"other":          "data.EntityTypeOther",
	"passive":        "data.EntityTypePassive",
	"player":         "data.EntityTypePlayer",
	"projectile":     "data.EntityTypeProjectile",
	"water_creature": "data.EntityTypeWaterCreature",
}
