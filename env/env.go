package env

import (
	"strings"

	"github.com/spf13/viper"
)

var envVarReplacer = strings.NewReplacer(".", "_", "-", "_")

func BindEnv(v *viper.Viper) {
	v.SetEnvKeyReplacer(envVarReplacer)
	v.AutomaticEnv()
}
