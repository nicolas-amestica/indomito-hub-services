package domain

import (
	"crypto/sha256"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
)

const legacyAgentProgramIDPrefix = "legacy-"

type legacyProgramSource struct {
	key          string
	fileName     string
	destinations []string
	region       string
	city         string
}

// BuildManagedProgramsFromLegacyKeys creates stable managed records without moving the source PDF files.
func BuildManagedProgramsFromLegacyKeys(keys []string, budgetPrefix, exceptionsPrefix string) ([]AgentProgram, error) {
	sources := make([]legacyProgramSource, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, exists := seen[key]; exists {
			continue
		}
		source, err := parseLegacyProgramKey(key, budgetPrefix, exceptionsPrefix)
		if err != nil {
			return nil, err
		}
		seen[key] = struct{}{}
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].key < sources[j].key })
	programs := make([]AgentProgram, 0, len(sources))
	for index, source := range sources {
		hash := sha256.Sum256([]byte(source.key))
		originRegions, originCities := []string{}, []string{}
		var origin string
		if source.city != "" {
			originCities = []string{source.city}
			origin = "salida desde " + displaySegment(source.city)
		} else {
			originRegions = []string{source.region}
			origin = "salida desde Región " + strings.ToUpper(source.region)
		}
		displays := make([]string, 0, len(source.destinations))
		for _, destination := range source.destinations {
			displays = append(displays, displaySegment(destination))
		}
		country := "Chile"
		if containsSegment(source.destinations, "brasil") {
			country = "Brasil"
		} else if containsSegment(source.destinations, "bariloche") {
			country = "Argentina"
			if len(source.destinations) > 1 {
				country = "Chile / Argentina"
			}
		}
		programs = append(programs, AgentProgram{
			ID: legacyAgentProgramIDPrefix + fmt.Sprintf("%x", hash[:8]), Name: strings.Join(displays, " · ") + " — " + origin,
			Destination: source.destinations[0], Destinations: source.destinations, Country: country,
			OriginRegions: originRegions, OriginCities: originCities,
			Description: "Programa y cotización histórica disponible para " + strings.Join(displays, ", ") + ", con " + origin + ".",
			Activities:  []string{}, DurationDays: 0, DocumentKey: source.key, FileName: source.fileName, Active: true, SortOrder: (index + 1) * 10,
		})
	}
	if err := VerifyLegacyProgramParity(keys, programs, budgetPrefix, exceptionsPrefix); err != nil {
		return nil, err
	}
	return programs, nil
}

// VerifyLegacyProgramParity proves that every legacy route resolves the exact same object keys.
func VerifyLegacyProgramParity(keys []string, programs []AgentProgram, budgetPrefix, exceptionsPrefix string) error {
	sources := make([]legacyProgramSource, 0, len(keys))
	for _, key := range keys {
		source, err := parseLegacyProgramKey(key, budgetPrefix, exceptionsPrefix)
		if err != nil {
			return err
		}
		sources = append(sources, source)
	}
	type route struct{ destination, region, city string }
	routes := make(map[route]struct{})
	for _, source := range sources {
		for _, destination := range source.destinations {
			routes[route{destination: destination, region: source.region, city: source.city}] = struct{}{}
		}
	}
	for candidate := range routes {
		expected := make([]string, 0)
		for _, source := range sources {
			if !containsSegment(source.destinations, candidate.destination) {
				continue
			}
			if candidate.city != "" {
				if source.city == candidate.city {
					expected = append(expected, source.key)
				}
			} else if source.city == "" && source.region == candidate.region {
				expected = append(expected, source.key)
			}
		}
		sort.Strings(expected)
		actual := managedBudgetKeys(programs, candidate.destination, candidate.region, candidate.city)
		if strings.Join(expected, "\n") != strings.Join(actual, "\n") {
			return fmt.Errorf("program parity failed for destination=%s region=%s city=%s: expected %v, got %v", candidate.destination, candidate.region, candidate.city, expected, actual)
		}
	}
	return nil
}

func parseLegacyProgramKey(key, budgetPrefix, exceptionsPrefix string) (legacyProgramSource, error) {
	key = strings.TrimPrefix(path.Clean("/"+key), "/")
	budgetPrefix = strings.Trim(strings.TrimSpace(budgetPrefix), "/") + "/"
	exceptionsPrefix = strings.Trim(strings.TrimSpace(exceptionsPrefix), "/") + "/"
	if !strings.HasSuffix(strings.ToLower(key), ".pdf") || !strings.HasPrefix(key, budgetPrefix) {
		return legacyProgramSource{}, fmt.Errorf("invalid legacy program key %q", key)
	}
	result := legacyProgramSource{key: key, fileName: path.Base(key)}
	var relative string
	if strings.HasPrefix(key, exceptionsPrefix) {
		relative = strings.TrimPrefix(key, exceptionsPrefix)
		parts := strings.Split(relative, "/")
		if len(parts) != 2 {
			return legacyProgramSource{}, fmt.Errorf("invalid legacy exception key %q", key)
		}
		result.city = normalizeSegment(parts[0])
	} else {
		relative = strings.TrimPrefix(key, budgetPrefix)
		parts := strings.Split(relative, "/")
		if len(parts) != 2 || parts[0] == "excepciones" {
			return legacyProgramSource{}, fmt.Errorf("invalid legacy regional key %q", key)
		}
		result.region = normalizeSegment(parts[0])
	}
	nameParts := strings.Split(strings.TrimSuffix(strings.ToLower(result.fileName), ".pdf"), "-")
	if len(nameParts) < 2 {
		return legacyProgramSource{}, fmt.Errorf("legacy program file has no correlative %q", key)
	}
	if _, err := strconv.Atoi(nameParts[len(nameParts)-1]); err != nil {
		return legacyProgramSource{}, fmt.Errorf("legacy program file has invalid correlative %q", key)
	}
	for _, destination := range nameParts[:len(nameParts)-1] {
		destination = normalizeSegment(destination)
		if destination != "" && !containsSegment(result.destinations, destination) {
			result.destinations = append(result.destinations, destination)
		}
	}
	if len(result.destinations) == 0 || (result.region == "" && result.city == "") {
		return legacyProgramSource{}, fmt.Errorf("legacy program key has incomplete routing %q", key)
	}
	return result, nil
}

func containsSegment(values []string, expected string) bool {
	for _, value := range values {
		if normalizeSegment(value) == expected {
			return true
		}
	}
	return false
}

func displaySegment(value string) string {
	replacer := strings.NewReplacer("_", " ")
	words := strings.Fields(replacer.Replace(value))
	for index, word := range words {
		if len(word) > 0 {
			words[index] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
