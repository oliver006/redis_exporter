package cfenv_test

import (
	"os"

	. "github.com/cloudfoundry-community/go-cfenv"
	"github.com/mitchellh/mapstructure"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Cfenv", func() {
	Describe("Application deserialization", func() {
		validEnv := []string{
			`VCAP_APPLICATION={"instance_id":"451f045fd16427bb99c895a2649b7b2a","application_id":"abcabc123123defdef456456","cf_api": "https://api.system_domain.com","instance_index":0,"host":"0.0.0.0","port":61857,"started_at":"2013-08-12 00:05:29 +0000","started_at_timestamp":1376265929,"start":"2013-08-12 00:05:29 +0000","state_timestamp":1376265929,"limits":{"mem":512,"disk":1024,"fds":16384},"application_version":"c1063c1c-40b9-434e-a797-db240b587d32","application_name":"styx-james","application_uris":["styx-james.a1-app.cf-app.com"],"version":"c1063c1c-40b9-434e-a797-db240b587d32","name":"styx-james","space_id":"3e0c28c5-6d9c-436b-b9ee-1f4326e54d05","space_name":"jdk","uris":["styx-james.a1-app.cf-app.com"],"users":null}`,
			`HOME=/home/vcap/app`,
			`MEMORY_LIMIT=512m`,
			`PWD=/home/vcap`,
			`TMPDIR=/home/vcap/tmp`,
			`USER=vcap`,
			`VCAP_SERVICES={"elephantsql-dev":[{"name":"elephantsql-dev-c6c60","label":"elephantsql-dev","tags":["New Product","relational","Data Store","postgresql"],"plan":"turtle","credentials":{"uri":"postgres://seilbmbd:PHxTPJSbkcDakfK4cYwXHiIX9Q8p5Bxn@babar.elephantsql.com:5432/seilbmbd"}}],"sendgrid":[{"name":"mysendgrid","label":"sendgrid","tags":["smtp","Email"],"plan":"free","credentials":{"hostname":"smtp.sendgrid.net","username":"QvsXMbJ3rK","password":"HCHMOYluTv"}}]}`,
		}

		validEnvWithoutSpaceIDAndName := []string{
			`VCAP_APPLICATION={"instance_id":"451f045fd16427bb99c895a2649b7b2a","application_id":"abcabc123123defdef456456","cf_api": "https://api.system_domain.com","instance_index":0,"host":"0.0.0.0","port":61857,"started_at":"2013-08-12 00:05:29 +0000","started_at_timestamp":1376265929,"start":"2013-08-12 00:05:29 +0000","state_timestamp":1376265929,"limits":{"mem":512,"disk":1024,"fds":16384},"application_version":"c1063c1c-40b9-434e-a797-db240b587d32","application_name":"styx-james","application_uris":["styx-james.a1-app.cf-app.com"],"version":"c1063c1c-40b9-434e-a797-db240b587d32","name":"styx-james","uris":["styx-james.a1-app.cf-app.com"],"users":null}`,
			`HOME=/home/vcap/app`,
			`MEMORY_LIMIT=512m`,
			`PWD=/home/vcap`,
			`TMPDIR=/home/vcap/tmp`,
			`USER=vcap`,
			`VCAP_SERVICES={"elephantsql-dev":[{"name":"elephantsql-dev-c6c60","label":"elephantsql-dev","tags":["New Product","relational","Data Store","postgresql"],"plan":"turtle","credentials":{"uri":"postgres://seilbmbd:PHxTPJSbkcDakfK4cYwXHiIX9Q8p5Bxn@babar.elephantsql.com:5432/seilbmbd"}}],"sendgrid":[{"name":"mysendgrid","label":"sendgrid","tags":["smtp","Email"],"plan":"free","credentials":{"hostname":"smtp.sendgrid.net","username":"QvsXMbJ3rK","password":"HCHMOYluTv"}}]}`,
		}

		envWithIntCredentials := []string{
			`VCAP_APPLICATION={"instance_id":"451f045fd16427bb99c895a2649b7b2a","application_id":"abcabc123123defdef456456","cf_api": "https://api.system_domain.com","instance_index":0,"host":"0.0.0.0","port":61857,"started_at":"2013-08-12 00:05:29 +0000","started_at_timestamp":1376265929,"start":"2013-08-12 00:05:29 +0000","state_timestamp":1376265929,"limits":{"mem":512,"disk":1024,"fds":16384},"application_version":"c1063c1c-40b9-434e-a797-db240b587d32","application_name":"styx-james","application_uris":["styx-james.a1-app.cf-app.com"],"version":"c1063c1c-40b9-434e-a797-db240b587d32","name":"styx-james","uris":["styx-james.a1-app.cf-app.com"],"users":null}`,
			`HOME=/home/vcap/app`,
			`MEMORY_LIMIT=512m`,
			`PWD=/home/vcap`,
			`TMPDIR=/home/vcap/tmp`,
			`USER=vcap`,
			`VCAP_SERVICES={"elephantsql-dev":[{"name":"elephantsql-dev-c6c60","label":"elephantsql-dev","tags":["New Product","relational","Data Store","postgresql"],"plan":"turtle","credentials":{"uri":"postgres://seilbmbd:PHxTPJSbkcDakfK4cYwXHiIX9Q8p5Bxn@babar.elephantsql.com:5432/seilbmbd"}}],"cloudantNoSQLDB": [{ "name": "my_cloudant", "label": "cloudantNoSQLDB", "plan": "Shared", "credentials": { "username": "18675309-0000-4aaa-bbbb-999999999-bluemix", "password": "18675309deadbeefaaaabbbbccccddddeeeeffff000099999999999999999999", "host": "01234567-9999-4999-aaaa-abcdefabcdef-bluemix.cloudant.com", "port": 443, "url": "https://18675309-0000-4aaa-bbbb-999999999-bluemix:18675309deadbeefaaaabbbbccccddddeeeeffff000099999999999999999999@01234567-9999-4999-aaaa-abcdefabcdef-bluemix.cloudant.com"}}],"sendgrid":[{"name":"mysendgrid","label":"sendgrid","tags":["smtp","Email"],"plan":"free","credentials":{"hostname":"smtp.sendgrid.net","username":"QvsXMbJ3rK","password":"HCHMOYluTv"}}]}`,
		}

		envWithArrayCredentials := []string{
			`VCAP_APPLICATION={}`,
			`VCAP_SERVICES={"p-kafka": [{"credentials": { "kafka" : { "port": 9092, "node_ips": ["10.244.9.2", "10.244.9.6", "10.244.9.10"]}}}]}`,
		}

		invalidEnv := []string{
			`VCAP_APPLICATION={"instance_index":0,"host":"0.0.0.0","port":61857,"started_at":"2013-08-12 00:05:29 +0000","started_at_timestamp":1376265929,"start":"2013-08-12 00:05:29 +0000","state_timestamp":1376265929,"limits":{"mem":512,"disk":1024,"fds":16384},"application_version":"c1063c1c-40b9-434e-a797-db240b587d32","application_name":"styx-james","application_uris":["styx-james.a1-app.cf-app.com"],"version":"c1063c1c-40b9-434e-a797-db240b587d32","name":"styx-james","uris":["styx-james.a1-app.cf-app.com"],"users":null}`,
			`HOME=/home/vcap/app`,
			`MEMORY_LIMIT_INVALID=512m`,
			`PWD=/home/vcap`,
			`TMPDIR=/home/vcap/tmp`,
			`USER=vcap`,
			`VCAP_SERVICES={"elephantsql-dev":[{"name":"","label":"elephantsql-dev","plan":"turtle","credentials":{"uri":"postgres://seilbmbd:PHxTPJSbkcDakfK4cYwXHiIX9Q8p5Bxn@babar.elephantsql.com:5432/seilbmbd"}}],"sendgrid":[{"name":"mysendgrid","label":"sendgrid","plan":"free","credentials":{"hostname":"smtp.sendgrid.net","username":"QvsXMbJ3rK","password":"HCHMOYluTv"}}]}`,
		}

		notCFEnv := []string{
			`HOME=/home/vcap/app`,
			`MEMORY_LIMIT_INVALID=512m`,
			`PWD=/home/vcap`,
			`PORT=1234`,
			`TMPDIR=/home/vcap/tmp`,
			`USER=vcap`,
		}

		cfEnv := []string{
			`VCAP_APPLICATION={"instance_id":"451f045fd16427bb99c895a2649b7b2a","application_id":"abcabc123123defdef456456","cf_api": "https://api.system_domain.com","instance_index":0,"host":"0.0.0.0","port":61857,"started_at":"2013-08-12 00:05:29 +0000","started_at_timestamp":1376265929,"start":"2013-08-12 00:05:29 +0000","state_timestamp":1376265929,"limits":{"mem":512,"disk":1024,"fds":16384},"application_version":"c1063c1c-40b9-434e-a797-db240b587d32","application_name":"styx-james","application_uris":["styx-james.a1-app.cf-app.com"],"version":"c1063c1c-40b9-434e-a797-db240b587d32","name":"styx-james","uris":["styx-james.a1-app.cf-app.com"],"users":null}`,
			`HOME=/home/vcap/app`,
			`MEMORY_LIMIT_INVALID=512m`,
			`PWD=/home/vcap`,
			`PORT=1234`,
			`TMPDIR=/home/vcap/tmp`,
			`USER=vcap`,
			`VCAP_SERVICES={"elephantsql-dev":[{"name":"","label":"elephantsql-dev","plan":"turtle","credentials":{"uri":"postgres://seilbmbd:PHxTPJSbkcDakfK4cYwXHiIX9Q8p5Bxn@babar.elephantsql.com:5432/seilbmbd"}}],"sendgrid":[{"name":"mysendgrid","label":"sendgrid","plan":"free","credentials":{"hostname":"smtp.sendgrid.net","username":"QvsXMbJ3rK","password":"HCHMOYluTv"}}]}`,
		}

		Context("When not running on Cloud Foundry", func() {
			It("IsRunningOnCF() returns false", func() {
				testEnv := Env(notCFEnv)
				_, err := New(testEnv)
				Ω(err).Should(HaveOccurred())
				Ω(IsRunningOnCF()).Should(BeFalse())
			})
		})

		Context("When running on Cloud Foundry", func() {
			BeforeEach(func() {
				os.Setenv("VCAP_APPLICATION", "{}")
			})
			AfterEach(func() {
				os.Unsetenv("VCAP_APPLICATION")
			})
			It("IsRunningOnCF() returns true", func() {
				testEnv := Env(cfEnv)
				_, err := New(testEnv)
				Ω(err).ShouldNot(HaveOccurred())
				Ω(IsRunningOnCF()).Should(BeTrue())
			})
		})

		Context("With valid environment", func() {
			It("Should deserialize correctly", func() {
				testEnv := Env(validEnv)
				cfenv, err := New(testEnv)
				Ω(err).Should(BeNil())
				Ω(cfenv).ShouldNot(BeNil())

				Ω(cfenv.ID).Should(BeEquivalentTo("451f045fd16427bb99c895a2649b7b2a"))
				Ω(cfenv.InstanceID).Should(BeEquivalentTo("451f045fd16427bb99c895a2649b7b2a"))
				Ω(cfenv.AppID).Should(BeEquivalentTo("abcabc123123defdef456456"))
				Ω(cfenv.CFAPI).Should(BeEquivalentTo("https://api.system_domain.com"))
				Ω(cfenv.Index).Should(BeEquivalentTo(0))
				Ω(cfenv.Name).Should(BeEquivalentTo("styx-james"))
				Ω(cfenv.SpaceName).Should(BeEquivalentTo("jdk"))
				Ω(cfenv.SpaceID).Should(BeEquivalentTo("3e0c28c5-6d9c-436b-b9ee-1f4326e54d05"))
				Ω(cfenv.Host).Should(BeEquivalentTo("0.0.0.0"))
				Ω(cfenv.Port).Should(BeEquivalentTo(61857))
				Ω(cfenv.Version).Should(BeEquivalentTo("c1063c1c-40b9-434e-a797-db240b587d32"))
				Ω(cfenv.Home).Should(BeEquivalentTo("/home/vcap/app"))
				Ω(cfenv.MemoryLimit).Should(BeEquivalentTo("512m"))
				Ω(cfenv.WorkingDir).Should(BeEquivalentTo("/home/vcap"))
				Ω(cfenv.TempDir).Should(BeEquivalentTo("/home/vcap/tmp"))
				Ω(cfenv.User).Should(BeEquivalentTo("vcap"))
				Ω(cfenv.Limits.Disk).Should(BeEquivalentTo(1024))
				Ω(cfenv.Limits.Mem).Should(BeEquivalentTo(512))
				Ω(cfenv.Limits.FDs).Should(BeEquivalentTo(16384))
				Ω(cfenv.ApplicationURIs[0]).Should(BeEquivalentTo("styx-james.a1-app.cf-app.com"))
				Ω(len(cfenv.Services)).Should(BeEquivalentTo(2))
				Ω(cfenv.Services["elephantsql-dev"][0].Name).Should(BeEquivalentTo("elephantsql-dev-c6c60"))
				Ω(cfenv.Services["elephantsql-dev"][0].Label).Should(BeEquivalentTo("elephantsql-dev"))
				Ω(cfenv.Services["elephantsql-dev"][0].Tags).Should(BeEquivalentTo([]string{"New Product", "relational", "Data Store", "postgresql"}))
				Ω(cfenv.Services["elephantsql-dev"][0].Plan).Should(BeEquivalentTo("turtle"))
				Ω(len(cfenv.Services["elephantsql-dev"][0].Credentials)).Should(BeEquivalentTo(1))
				Ω(cfenv.Services["elephantsql-dev"][0].Credentials["uri"]).Should(BeEquivalentTo("postgres://seilbmbd:PHxTPJSbkcDakfK4cYwXHiIX9Q8p5Bxn@babar.elephantsql.com:5432/seilbmbd"))
				Ω(cfenv.Services["sendgrid"][0].Name).Should(BeEquivalentTo("mysendgrid"))
				Ω(cfenv.Services["sendgrid"][0].Label).Should(BeEquivalentTo("sendgrid"))
				Ω(cfenv.Services["sendgrid"][0].Tags).Should(BeEquivalentTo([]string{"smtp", "Email"}))
				Ω(cfenv.Services["sendgrid"][0].Plan).Should(BeEquivalentTo("free"))
				Ω(len(cfenv.Services["sendgrid"][0].Credentials)).Should(BeEquivalentTo(3))
				Ω(cfenv.Services["sendgrid"][0].Credentials["hostname"]).Should(BeEquivalentTo("smtp.sendgrid.net"))
				Ω(cfenv.Services["sendgrid"][0].Credentials["username"]).Should(BeEquivalentTo("QvsXMbJ3rK"))
				Ω(cfenv.Services["sendgrid"][0].Credentials["password"]).Should(BeEquivalentTo("HCHMOYluTv"))

				name, err := cfenv.Services.WithName("elephantsql-dev-c6c60")
				Ω(name.Name).Should(BeEquivalentTo("elephantsql-dev-c6c60"))
				Ω(err).Should(BeNil())

				tag, err := cfenv.Services.WithTag("postgresql")
				Ω(len(tag)).Should(BeEquivalentTo(1))
				Ω(tag[0].Tags).Should(ContainElement("postgresql"))
				Ω(err).Should(BeNil())

				label, err := cfenv.Services.WithLabel("elephantsql-dev")
				Ω(len(label)).Should(BeEquivalentTo(1))
				Ω(label[0].Label).Should(BeEquivalentTo("elephantsql-dev"))
				Ω(err).Should(BeNil())

				names, err := cfenv.Services.WithNameUsingPattern(".*(sql|mysend).*")
				Ω(len(names)).Should(BeEquivalentTo(2))
				Ω(err).Should(BeNil())
				isValidNames := true
				for _, service := range names {
					if service.Name != "mysendgrid" && service.Name != "elephantsql-dev-c6c60" {
						isValidNames = false
					}
				}
				Ω(isValidNames).Should(BeTrue(), "Not valid names when finding by regex")

				tags, err := cfenv.Services.WithTagUsingPattern(".*s.*")
				Ω(len(tags)).Should(BeEquivalentTo(2))
				Ω(err).Should(BeNil())
				isValidTags := true
				for _, service := range tags {
					if service.Name != "mysendgrid" && service.Name != "elephantsql-dev-c6c60" {
						isValidTags = false
					}
				}
				Ω(isValidTags).Should(BeTrue(), "Not valid tags when finding by regex")

			})

			It("Should prefer the PORT environment variable over VCAP_APPLICATION.PORT", func() {
				validEnv = append(validEnv, "PORT=12345")
				testEnv := Env(validEnv)
				cfenv, err := New(testEnv)
				Ω(err).Should(BeNil())
				Ω(cfenv).ShouldNot(BeNil())
				Ω(cfenv.Port).Should(BeEquivalentTo(12345))
			})
		})

		Context("Without a space name and id", func() {
			It("Should deserialize correctly", func() {
				testEnv := Env(validEnvWithoutSpaceIDAndName)
				cfenv, err := New(testEnv)
				Ω(err).Should(BeNil())
				Ω(cfenv).ShouldNot(BeNil())
				Ω(cfenv.SpaceID).Should(BeEmpty())
				Ω(cfenv.SpaceName).Should(BeEmpty())
			})
		})

		Context("With valid environment with a service with credentials that are an array", func() {
			It("should deserialize correctly", func() {
				testEnv := Env(envWithArrayCredentials)
				cfenv, err := New(testEnv)
				Ω(err).Should(BeNil())
				Ω(cfenv).ShouldNot(BeNil())

				credential := map[string]interface{}{}
				mapstructure.Decode(cfenv.Services["p-kafka"][0].Credentials["kafka"], &credential)

				Ω(len(cfenv.Services["p-kafka"][0].Credentials)).Should(BeEquivalentTo(1))
				Ω(credential["node_ips"]).Should(BeEquivalentTo([]interface{}{"10.244.9.2", "10.244.9.6", "10.244.9.10"}))
				Ω(credential["port"]).Should(BeEquivalentTo(9092))
			})
		})

		Context("With valid environment with a service with credentials with a port that is an int", func() {
			It("Should deserialize correctly", func() {
				testEnv := Env(envWithIntCredentials)
				cfenv, err := New(testEnv)
				Ω(err).Should(BeNil())
				Ω(cfenv).ShouldNot(BeNil())

				Ω(cfenv.ID).Should(BeEquivalentTo("451f045fd16427bb99c895a2649b7b2a"))
				Ω(cfenv.Index).Should(BeEquivalentTo(0))
				Ω(cfenv.Name).Should(BeEquivalentTo("styx-james"))
				Ω(cfenv.Host).Should(BeEquivalentTo("0.0.0.0"))
				Ω(cfenv.Port).Should(BeEquivalentTo(61857))
				Ω(cfenv.Version).Should(BeEquivalentTo("c1063c1c-40b9-434e-a797-db240b587d32"))
				Ω(cfenv.Home).Should(BeEquivalentTo("/home/vcap/app"))
				Ω(cfenv.MemoryLimit).Should(BeEquivalentTo("512m"))
				Ω(cfenv.WorkingDir).Should(BeEquivalentTo("/home/vcap"))
				Ω(cfenv.TempDir).Should(BeEquivalentTo("/home/vcap/tmp"))
				Ω(cfenv.User).Should(BeEquivalentTo("vcap"))
				Ω(cfenv.ApplicationURIs[0]).Should(BeEquivalentTo("styx-james.a1-app.cf-app.com"))
				Ω(len(cfenv.Services)).Should(BeEquivalentTo(3))

				Ω(cfenv.Services["elephantsql-dev"][0].Name).Should(BeEquivalentTo("elephantsql-dev-c6c60"))
				Ω(cfenv.Services["elephantsql-dev"][0].Label).Should(BeEquivalentTo("elephantsql-dev"))
				Ω(cfenv.Services["elephantsql-dev"][0].Tags).Should(BeEquivalentTo([]string{"New Product", "relational", "Data Store", "postgresql"}))
				Ω(cfenv.Services["elephantsql-dev"][0].Plan).Should(BeEquivalentTo("turtle"))
				Ω(len(cfenv.Services["elephantsql-dev"][0].Credentials)).Should(BeEquivalentTo(1))
				Ω(cfenv.Services["elephantsql-dev"][0].Credentials["uri"]).Should(BeEquivalentTo("postgres://seilbmbd:PHxTPJSbkcDakfK4cYwXHiIX9Q8p5Bxn@babar.elephantsql.com:5432/seilbmbd"))

				Ω(cfenv.Services["cloudantNoSQLDB"][0].Name).Should(BeEquivalentTo("my_cloudant"))
				Ω(cfenv.Services["cloudantNoSQLDB"][0].Label).Should(BeEquivalentTo("cloudantNoSQLDB"))
				Ω(cfenv.Services["cloudantNoSQLDB"][0].Plan).Should(BeEquivalentTo("Shared"))
				Ω(len(cfenv.Services["cloudantNoSQLDB"][0].Credentials)).Should(BeEquivalentTo(5))
				Ω(cfenv.Services["cloudantNoSQLDB"][0].Credentials["port"]).Should(BeEquivalentTo(443))

				Ω(cfenv.Services["sendgrid"][0].Name).Should(BeEquivalentTo("mysendgrid"))
				Ω(cfenv.Services["sendgrid"][0].Label).Should(BeEquivalentTo("sendgrid"))
				Ω(cfenv.Services["sendgrid"][0].Tags).Should(BeEquivalentTo([]string{"smtp", "Email"}))
				Ω(cfenv.Services["sendgrid"][0].Plan).Should(BeEquivalentTo("free"))
				Ω(len(cfenv.Services["sendgrid"][0].Credentials)).Should(BeEquivalentTo(3))
				Ω(cfenv.Services["sendgrid"][0].Credentials["hostname"]).Should(BeEquivalentTo("smtp.sendgrid.net"))
				Ω(cfenv.Services["sendgrid"][0].Credentials["username"]).Should(BeEquivalentTo("QvsXMbJ3rK"))
				Ω(cfenv.Services["sendgrid"][0].Credentials["password"]).Should(BeEquivalentTo("HCHMOYluTv"))

				name, err := cfenv.Services.WithName("elephantsql-dev-c6c60")
				Ω(name.Name).Should(BeEquivalentTo("elephantsql-dev-c6c60"))
				Ω(err).Should(BeNil())

				tag, err := cfenv.Services.WithTag("postgresql")
				Ω(len(tag)).Should(BeEquivalentTo(1))
				Ω(tag[0].Tags).Should(ContainElement("postgresql"))
				Ω(err).Should(BeNil())

				label, err := cfenv.Services.WithLabel("elephantsql-dev")
				Ω(len(label)).Should(BeEquivalentTo(1))
				Ω(label[0].Label).Should(BeEquivalentTo("elephantsql-dev"))
				Ω(err).Should(BeNil())

				names, err := cfenv.Services.WithNameUsingPattern(".*(sql|my_cloud).*")
				Ω(len(names)).Should(BeEquivalentTo(2))
				Ω(err).Should(BeNil())
				isValidNames := true
				for _, service := range names {
					if service.Name != "my_cloudant" && service.Name != "elephantsql-dev-c6c60" {
						isValidNames = false
					}
				}
				Ω(isValidNames).Should(BeTrue(), "Not valid names when finding by regex")

				tags, err := cfenv.Services.WithTagUsingPattern(".*s.*")
				Ω(len(tags)).Should(BeEquivalentTo(2))
				Ω(err).Should(BeNil())
				isValidTags := true
				for _, service := range tags {
					if service.Name != "mysendgrid" && service.Name != "elephantsql-dev-c6c60" {
						isValidTags = false
					}
				}
				Ω(isValidTags).Should(BeTrue(), "Not valid tags when finding by regex")

			})
		})

		Context("With invalid environment", func() {
			It("Should deserialize correctly, with missing values", func() {
				testEnv := Env(invalidEnv)
				cfenv, err := New(testEnv)
				Ω(err).Should(BeNil())
				Ω(cfenv).ShouldNot(BeNil())

				Ω(cfenv.ID).Should(BeEquivalentTo(""))
				Ω(cfenv.Index).Should(BeEquivalentTo(0))
				Ω(cfenv.Name).Should(BeEquivalentTo("styx-james"))
				Ω(cfenv.Host).Should(BeEquivalentTo("0.0.0.0"))
				Ω(cfenv.Port).Should(BeEquivalentTo(61857))
				Ω(cfenv.Version).Should(BeEquivalentTo("c1063c1c-40b9-434e-a797-db240b587d32"))
				Ω(cfenv.Home).Should(BeEquivalentTo("/home/vcap/app"))
				Ω(cfenv.MemoryLimit).Should(BeEquivalentTo(""))
				Ω(cfenv.WorkingDir).Should(BeEquivalentTo("/home/vcap"))
				Ω(cfenv.TempDir).Should(BeEquivalentTo("/home/vcap/tmp"))
				Ω(cfenv.User).Should(BeEquivalentTo("vcap"))
				Ω(cfenv.ApplicationURIs[0]).Should(BeEquivalentTo("styx-james.a1-app.cf-app.com"))
				Ω(len(cfenv.Services)).Should(BeEquivalentTo(2))
				Ω(len(cfenv.Services)).Should(BeEquivalentTo(2))
				Ω(cfenv.Services["elephantsql-dev"][0].Name).Should(BeEquivalentTo(""))
				Ω(cfenv.Services["elephantsql-dev"][0].Label).Should(BeEquivalentTo("elephantsql-dev"))
				Ω(cfenv.Services["elephantsql-dev"][0].Plan).Should(BeEquivalentTo("turtle"))
				Ω(len(cfenv.Services["elephantsql-dev"][0].Credentials)).Should(BeEquivalentTo(1))
				Ω(cfenv.Services["elephantsql-dev"][0].Credentials["uri"]).Should(BeEquivalentTo("postgres://seilbmbd:PHxTPJSbkcDakfK4cYwXHiIX9Q8p5Bxn@babar.elephantsql.com:5432/seilbmbd"))

				Ω(cfenv.Services["sendgrid"][0].Name).Should(BeEquivalentTo("mysendgrid"))
				Ω(cfenv.Services["sendgrid"][0].Label).Should(BeEquivalentTo("sendgrid"))
				Ω(cfenv.Services["sendgrid"][0].Plan).Should(BeEquivalentTo("free"))
				Ω(len(cfenv.Services["sendgrid"][0].Credentials)).Should(BeEquivalentTo(3))
				Ω(cfenv.Services["sendgrid"][0].Credentials["hostname"]).Should(BeEquivalentTo("smtp.sendgrid.net"))
				Ω(cfenv.Services["sendgrid"][0].Credentials["username"]).Should(BeEquivalentTo("QvsXMbJ3rK"))
				Ω(cfenv.Services["sendgrid"][0].Credentials["password"]).Should(BeEquivalentTo("HCHMOYluTv"))
			})
		})
	})

	Describe("CredentialString", func() {
		var service = Service{
			Credentials: map[string]interface{}{
				"string": "stringy-credential",
				"int":    42,
				"nested": map[string]string{
					"key": "value",
				},
			},
		}

		It("returns the requested credential as a string when the credential is a string", func() {
			result, ok := service.CredentialString("string")
			Expect(ok).To(BeTrue())
			Expect(result).To(Equal("stringy-credential"))
		})

		It("returns false when the credential is not a string", func() {
			_, ok := service.CredentialString("int")
			Expect(ok).To(BeFalse())
		})

		It("returns false when the credential is a nested thing", func() {
			_, ok := service.CredentialString("nested")
			Expect(ok).To(BeFalse())
		})
	})
})
