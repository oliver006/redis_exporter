package main

import (
	"context"
	"errors"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/redis/mgmt/2018-03-01/redis"
	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2018-02-01/resources"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	log "github.com/sirupsen/logrus"
)

// AzureCredentials contains connection credentials
type AzureCredentials struct {
	environment    string
	subscriptionID string
	tenantID       string
	clientID       string
	clientSecret   string
}

// AzureRedisCredentials contains redis credentials
type AzureRedisCredentials struct {
	alias string
	url   string
	key   string
}

// AzureRedises struct
type AzureRedises struct {
	sp      *AzureCredentials
	redises *[]AzureRedisCredentials
}

// NewAzureRedises class
func NewAzureRedises(azCreds *AzureCredentials) *AzureRedises {
	return &AzureRedises{
		sp: azCreds,
	}
}

// StartUpdateRoutine start background updating of creds list
func (r *AzureRedises) StartUpdateRoutine() {
	go func() {
		var err error
		for {
			log.Debugf("Fetching Azure credentials for redises")
			err = r.updateRedisesList()
			if err != nil {
				log.Fatalf("Error fetching Azure Redis Services: %s", err)
			}
			log.Debugf("Fetched Azure credentials for %d redises", len(*r.redises))
			time.Sleep(1 * time.Minute)
		}
	}()
}

// RedisesExists return true if redises list is not empty
func (r *AzureRedises) RedisesExists() bool {
	return r.redises != nil && len(*r.redises) > 0
}

// GetRedisCredentialsByAlias return element if exists
func (r *AzureRedises) GetRedisCredentialsByAlias(alias string) (AzureRedisCredentials, bool) {
	if !r.RedisesExists() {
		return AzureRedisCredentials{}, false
	}

	for _, v := range *r.redises {
		if v.alias == alias {
			return v, true
		}
	}

	return AzureRedisCredentials{}, false
}

// newAzureAutorizer creates azure autorizer to process service discovery
func (r *AzureRedises) newAzureAutorizer() (azure.Environment, autorest.Authorizer, error) {
	env := azure.Environment{}

	if r.sp.environment == "" || r.sp.subscriptionID == "" || r.sp.clientID == "" || r.sp.clientSecret == "" || r.sp.tenantID == "" {
		return env, nil, errors.New("Azure credentials required")
	}

	clientCreds := auth.NewClientCredentialsConfig(r.sp.clientID, r.sp.clientSecret, r.sp.tenantID)
	env, err := azure.EnvironmentFromName(r.sp.environment)

	if err != nil {
		return env, nil, err
	}

	autorizer, err := clientCreds.Authorizer()

	return env, autorizer, err
}

// updateRedisesList update AzureRedises.redises list
func (r *AzureRedises) updateRedisesList() error {
	var redList []AzureRedisCredentials

	env, autorizer, err := r.newAzureAutorizer()

	if err != nil {
		return err
	}

	redisClient := redis.NewClientWithBaseURI(env.ResourceManagerEndpoint, r.sp.subscriptionID)
	redisClient.Authorizer = autorizer

	groupClient := resources.NewGroupsClientWithBaseURI(env.ResourceManagerEndpoint, r.sp.subscriptionID)
	groupClient.Authorizer = autorizer

	groupsList, err := groupClient.List(context.Background(), "", nil)

	if err != nil {
		return err
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

			redList = append(redList, cred)
		}
	}

	r.redises = &redList
	return nil
}
