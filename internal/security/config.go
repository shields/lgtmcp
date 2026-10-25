// Copyright © 2026 Michael Shields
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package security

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/viper"
	"github.com/zricethezav/gitleaks/v8/config"
)

// RepoConfigName is the gitleaks configuration file looked up at a
// repository's root, as the gitleaks CLI does for its scan target.
const RepoConfigName = ".gitleaks.toml"

var (
	// ErrInvalidConfig indicates a gitleaks config that failed to parse or
	// translate.
	ErrInvalidConfig = errors.New("invalid gitleaks config")
	// ErrExtendUnsupported indicates a gitleaks config whose [extend] section
	// names a path or URL; only extend.useDefault is supported.
	ErrExtendUnsupported = errors.New(
		"gitleaks config: only extend.useDefault is supported, not extend.path or extend.url")
	// ErrUnknownTargetRule indicates an [[allowlists]] entry whose targetRules
	// names a rule that neither the config nor the default rules define.
	ErrUnknownTargetRule = errors.New("gitleaks config: [[allowlists]] target rule does not exist")
	// ErrConfigTooLarge indicates a repository config above maxRepoConfigSize.
	ErrConfigTooLarge = errors.New("gitleaks config: file too large")
)

// maxRepoConfigSize bounds how much of a repository's config is parsed, since
// the repository, not the operator, controls that file.
const maxRepoConfigSize = 1 << 20

// translateMutex serializes the library's Translate, which reads package-level
// viper state.
var translateMutex sync.Mutex

// defaultConfig returns the translated built-in gitleaks rules, parsed once per
// process.
var defaultConfig = sync.OnceValues(func() (config.Config, error) {
	vc, err := parseTOML(config.DefaultConfig)
	if err != nil {
		return config.Config{}, err
	}

	return translate(vc)
})

// loadConfig translates TOML config content into a gitleaks Config, applying
// extend.useDefault itself.
//
// The library's Translate can extend a config with the default rules on its
// own, but it counts extensions in a package-level variable that is never
// reset: the first two configs translated in a process are extended and every
// later one silently is not, and after the first the targeted allowlists are
// dropped. A server that loads a config per repository cannot rely on it, so
// the merge is done here with the same rules as the library's extend.
func loadConfig(content string) (config.Config, error) {
	vc, err := parseTOML(content)
	if err != nil {
		return config.Config{}, err
	}
	if vc.Extend.Path != "" || vc.Extend.URL != "" {
		return config.Config{}, ErrExtendUnsupported
	}
	extend := vc.Extend
	vc.Extend = config.Extend{}

	var def config.Config
	if extend.UseDefault {
		def, err = defaultConfig()
		if err != nil {
			return config.Config{}, err
		}
		completeRedefinedRules(&vc, def)
	}

	// Allowlists with targetRules may name default rules, which exist only
	// after the merge, so they are translated separately and attached last.
	var (
		targets    [][]string
		targetedVC config.ViperConfig
	)
	kept := 0
	for _, a := range vc.Allowlists {
		if len(a.TargetRules) == 0 {
			vc.Allowlists[kept] = a
			kept++

			continue
		}
		targets = append(targets, a.TargetRules)
		a.TargetRules = nil
		targetedVC.Allowlists = append(targetedVC.Allowlists, a)
	}
	vc.Allowlists = vc.Allowlists[:kept]

	cfg, err := translate(vc)
	if err != nil {
		return config.Config{}, err
	}
	if extend.UseDefault {
		mergeDefault(&cfg, def, extend.DisabledRules)
	}

	if len(targets) == 0 {
		return cfg, nil
	}
	targetedCfg, err := translate(targetedVC)
	if err != nil {
		return config.Config{}, err
	}
	for i, allowlist := range targetedCfg.Allowlists {
		for _, id := range targets[i] {
			rule, ok := cfg.Rules[id]
			if !ok {
				return config.Config{}, fmt.Errorf("%w: %q", ErrUnknownTargetRule, id)
			}
			rule.Allowlists = append(slices.Clone(rule.Allowlists), allowlist)
			cfg.Rules[id] = rule
		}
	}

	return cfg, nil
}

// completeRedefinedRules gives a rule that redefines a default rule without a
// regex or path the default's pattern. The library validates a config's rules
// only after extending it, so such a partial redefinition is valid for the
// gitleaks CLI, but Translate runs here before the merge and would reject it;
// the merge keeps the config's pattern over the default's, so the result is
// the same.
func completeRedefinedRules(vc *config.ViperConfig, def config.Config) {
	for i := range vc.Rules {
		rule := &vc.Rules[i]
		if rule.Regex != "" || rule.Path != "" {
			continue
		}
		base, ok := def.Rules[rule.ID]
		if !ok {
			continue
		}
		if base.Regex != nil {
			rule.Regex = base.Regex.String()
		} else if base.Path != nil {
			rule.Path = base.Path.String()
		}
	}
}

// parseTOML reads TOML config content into the library's intermediate form
// through a private viper instance, so concurrent loads do not share state.
func parseTOML(content string) (config.ViperConfig, error) {
	v := viper.New()
	v.SetConfigType("toml")
	if err := v.ReadConfig(strings.NewReader(content)); err != nil {
		return config.ViperConfig{}, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}
	var vc config.ViperConfig
	if err := v.Unmarshal(&vc); err != nil {
		return config.ViperConfig{}, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}

	return vc, nil
}

// translate runs the library's Translate. Translate panics on an invalid
// regular expression instead of returning an error, so the panic is turned
// into one here to keep a bad repository config from taking the server down.
func translate(vc config.ViperConfig) (config.Config, error) {
	translateMutex.Lock()
	defer translateMutex.Unlock()

	var (
		cfg config.Config
		err error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%w: %v", ErrInvalidConfig, r)
			}
		}()
		cfg, err = vc.Translate()
	}()
	if err != nil {
		if errors.Is(err, ErrInvalidConfig) {
			return config.Config{}, err
		}

		return config.Config{}, fmt.Errorf("%w: %w", ErrInvalidConfig, err)
	}

	return cfg, nil
}

// mergeDefault adds the default rules and allowlists to cfg the way the
// library's extend does: a rule the config already defines keeps its own
// non-empty fields and gains the default's lists, and disabled rules are left
// out. Slices are copied rather than appended in place so the cached default
// config is never modified.
func mergeDefault(cfg *config.Config, def config.Config, disabled []string) {
	for id, base := range def.Rules {
		if slices.Contains(disabled, id) {
			continue
		}
		current, ok := cfg.Rules[id]
		if !ok {
			cfg.Rules[id] = base
			for _, k := range base.Keywords {
				cfg.Keywords[k] = struct{}{}
			}
			cfg.OrderedRules = append(cfg.OrderedRules, id)

			continue
		}
		merged := base
		if current.Description != "" {
			merged.Description = current.Description
		}
		if current.Entropy != 0 {
			merged.Entropy = current.Entropy
		}
		if current.SecretGroup != 0 {
			merged.SecretGroup = current.SecretGroup
		}
		if current.Regex != nil {
			merged.Regex = current.Regex
		}
		if current.Path != nil {
			merged.Path = current.Path
		}
		merged.Tags = slices.Concat(base.Tags, current.Tags)
		merged.Keywords = slices.Concat(base.Keywords, current.Keywords)
		merged.Allowlists = slices.Concat(base.Allowlists, current.Allowlists)
		for _, k := range merged.Keywords {
			cfg.Keywords[k] = struct{}{}
		}
		cfg.Rules[id] = merged
	}
	cfg.Allowlists = slices.Concat(cfg.Allowlists, def.Allowlists)
	slices.Sort(cfg.OrderedRules)
}
