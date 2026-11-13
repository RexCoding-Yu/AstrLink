package controlapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// validateConfigTime bounds a plugin's validateConfig.
const validateConfigTime = 5 * time.Second

// checkRequestPlugins readies the request processing instances a save
// gives: a new one gets an id, and each must name a plugin with a request
// processing part whose schema and validateConfig take its config.
func (handler *Handler) checkRequestPlugins(ctx context.Context, instances []contract.RequestPluginInstance) error {
	for index := range instances {
		instance := &instances[index]
		if instance.ID == "" {
			instance.ID = newRequestPluginInstanceID()
		}
		instance.Name = strings.TrimSpace(instance.Name)
		if instance.Models == nil {
			instance.Models = []string{}
		}
		for at, model := range instance.Models {
			instance.Models[at] = strings.TrimSpace(model)
		}
		if err := instance.Validate(); err != nil {
			return err
		}
		if handler.plugins == nil {
			return fmt.Errorf("plugins are unavailable")
		}
		target := handler.plugins.Current().Plugin(instance.Plugin)
		if target == nil || target.NewAPI() == nil || !target.NewAPI().HasRequestPart() {
			return fmt.Errorf("%s: %s is not an installed plugin with a request processing part", instance.Name, instance.Plugin)
		}
		meta := target.NewAPI()
		checked, err := meta.Config().CheckConfig(instance.Config)
		if err != nil {
			return fmt.Errorf("%s: the config %v", instance.Name, err)
		}
		if target.HasTaskHook("validateConfig") {
			config, _ := meta.Config().HookConfig(checked)
			call, cancel := context.WithTimeout(ctx, validateConfigTime)
			_, _, err := target.CallTask(call, "validateConfig", []any{json.RawMessage(config)}, plugin.CallOptions{})
			cancel()
			if err != nil {
				return fmt.Errorf("%s: %s", instance.Name, validateConfigMessage(err))
			}
		}
	}
	return nil
}

// validateConfigMessage is what a throw of validateConfig says, cut to 256
// characters.
func validateConfigMessage(err error) string {
	message := err.Error()
	if callErr, ok := err.(*plugin.CallError); ok {
		if callErr.Thrown != "" {
			message = callErr.Thrown
		} else if callErr.Message != "" {
			message = callErr.Message
		}
	}
	if at := strings.Index(message, " (at "); at > 0 {
		message = message[:at]
	}
	message = strings.TrimPrefix(message, "threw Error: ")
	if runes := []rune(message); len(runes) > 256 {
		message = string(runes[:256])
	}
	return message
}

func newRequestPluginInstanceID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var random [16]byte
	_, _ = rand.Read(random[:])
	id := make([]byte, len(random))
	for index, value := range random {
		id[index] = alphabet[int(value)%len(alphabet)]
	}
	return "rpi_" + string(id)
}
