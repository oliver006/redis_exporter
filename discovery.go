package main

import (
	"context"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/redis/mgmt/2018-03-01/redis"
	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2018-02-01/resources"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	log "github.com/sirupsen/logrus"
)

// AzureRedisCredentials contains connection credentials
type AzureRedisCredentials struct {
	alias string
	url   string
	key   string
}

// UpdateAzureRedisServices start background updating of creds list
func UpdateAzureRedisServices(credsList *[]AzureRedisCredentials) {
	go func() {
		for {
			log.Debugf("Fetching Azure credentials for redises")
			var err error
			*credsList, err = GetAzureRedisServices()
			if err != nil {
				log.Fatalf("Error fetch Azure Redis Services %s    err: %s", *credsList, err)
			}
			log.Debugf("Fetched Azure credentials for %d redises", len(*credsList))
			time.Sleep(1 * time.Hour)
		}
	}()
}

// GetAzureRedisServices returns an arrays of creds
func GetAzureRedisServices() ([]AzureRedisCredentials, error) {
	var credList []AzureRedisCredentials

	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err != nil {
		return nil, err
	}
	env, _ := azure.EnvironmentFromName(os.Getenv("AZURE_ENVIRONMENT"))
	if err != nil {
		return nil, err
	}
	redisClient := redis.NewClientWithBaseURI(env.ResourceManagerEndpoint, os.Getenv("AZURE_SUBSCRIPTION_ID"))
	redisClient.Authorizer = authorizer

	groupClient := resources.NewGroupsClientWithBaseURI(env.ResourceManagerEndpoint, os.Getenv("AZURE_SUBSCRIPTION_ID"))
	groupClient.Authorizer = authorizer

	groupsList, err := groupClient.List(context.Background(), "", nil)

	if err != nil {
		return nil, err
	}
	for _, resourceGroup := range groupsList.Values() {
		listResultPage, _ := redisClient.ListByResourceGroup(context.Background(), *resourceGroup.Name)
		for _, cache := range listResultPage.Values() {
			keys, _ := redisClient.ListKeys(context.Background(), *resourceGroup.Name, *cache.Name)
			EnableNonSslPort := *cache.Properties.EnableNonSslPort
			cred := AzureRedisCredentials{}

			if EnableNonSslPort {
				cred.url = "redis://" + *cache.Properties.HostName
			} else {
				cred.url = "rediss://" + *cache.Properties.HostName
			}
			if keys.PrimaryKey == nil {
				log.Warnf("ERROR: You have no rights to read redis keys for %s\n", *cache.Name)
			}
			cred.alias = *cache.Name
			cred.key = *keys.PrimaryKey

			credList = append(credList, cred)
		}
	}
	return credList, nil
}
