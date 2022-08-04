package svix

import (
	"fmt"

	"github.com/numary/webhooks-cloud/cmd/constants"
	"github.com/spf13/viper"
	svix "github.com/svix/svix-webhooks/go"
)

func New() (*svix.Svix, string, error) {
	token := viper.GetString(constants.SvixTokenFlag)
	if token == "" {
		token = constants.DefaultSvixToken
	}

	appId := viper.GetString(constants.SvixAppIdFlag)
	if appId == "" {
		appId = constants.DefaultSvixAppId
	}

	svixClient := svix.New(token, nil)
	_, err := svixClient.Application.Get(appId)
	if err != nil {
		return nil, "", fmt.Errorf("could not get svix app %s: %w", appId, err)
	}

	return svixClient, appId, nil
}
