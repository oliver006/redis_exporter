package exporter

/*
  to run the tests with redis running on anything but localhost:6379 use
  $ go test   --redis.addr=<host>:<port>

  for html coverage report run
  $ go test -coverprofile=coverage.out  && go tool cover -html=coverage.out
*/

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/garyburd/redigo/redis"
)

var (
	keys         = []string{}
	keysExpiring = []string{}
	ts           = int32(time.Now().Unix())
	r            = RedisHost{}

	dbNumStr     = "11"
	dbNumStrFull = fmt.Sprintf("db%d", dbNumStr)
)

func setupDBKeys(t *testing.T) error {

	c, err := redis.Dial("tcp", r.Addrs[0])
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("SELECT", dbNumStr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	for _, key := range keys {
		_, err = c.Do("SET", key, "value")
		if err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	// setting to expire in 300 seconds, should be plenty for a test run
	for _, key := range keysExpiring {
		_, err = c.Do("SETEX", key, "300", "value")
		if err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	time.Sleep(time.Millisecond * 150)

	return nil
}

func deleteKeysFromDB(t *testing.T) error {

	c, err := redis.Dial("tcp", r.Addrs[0])
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("SELECT", dbNumStr)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	for _, key := range keys {
		c.Do("DEL", key)
	}

	for _, key := range keysExpiring {
		c.Do("DEL", key)
	}

	return nil
}

func TestCountingKeys(t *testing.T) {

	e := NewRedisExporter(r, "test")

	scrapes := make(chan scrapeResult)
	go e.scrape(scrapes)

	var keysTestDB float64 = 0
	for s := range scrapes {
		if s.Name == "db_keys_total" && s.DB == dbNumStrFull {
			keysTestDB = s.Value
			break
		}
	}

	setupDBKeys(t)
	defer deleteKeysFromDB(t)

	scrapes = make(chan scrapeResult)
	go e.scrape(scrapes)

	want := keysTestDB + float64(len(keys)) + float64(len(keysExpiring))

	for s := range scrapes {
		if s.Name == "db_keys_total" && s.DB == dbNumStrFull {
			if want != s.Value {
				t.Errorf("values not matching, %f != %f", keysTestDB, s.Value)
			}
			break
		}
	}

	deleteKeysFromDB(t)
	scrapes = make(chan scrapeResult)
	go e.scrape(scrapes)

	for s := range scrapes {
		if s.Name == "db_keys_total" && s.DB == dbNumStrFull {
			if keysTestDB != s.Value {
				t.Errorf("values not matching, %f != %f", keysTestDB, s.Value)
			}
			break
		}
		if s.Name == "db_avg_ttl_seconds" && s.DB == dbNumStrFull {
			if keysTestDB != s.Value {
				t.Errorf("values not matching, %f != %f", keysTestDB, s.Value)
			}
			break
		}
	}
}

func TestExporterMetrics(t *testing.T) {

	e := NewRedisExporter(r, "test")

	setupDBKeys(t)
	defer deleteKeysFromDB(t)

	scrapes := make(chan scrapeResult)
	go e.scrape(scrapes)

	e.setMetrics(scrapes)

	want := 25
	if len(e.metrics) < want {
		t.Errorf("need moar metrics, found %d, want > %d", len(e.metrics), want)
	}

	wantKeys := []string{"db_keys_total", "db_avg_ttl_seconds", "instantaneous_ops_per_sec", "used_cpu_sys"}

	for _, k := range wantKeys {
		if _, ok := e.metrics[k]; !ok {
			t.Errorf("missing metrics key: %s", k)
		}
	}

}

func TestExporterValues(t *testing.T) {

	e := NewRedisExporter(r, "test")

	setupDBKeys(t)
	defer deleteKeysFromDB(t)

	scrapes := make(chan scrapeResult)
	go e.scrape(scrapes)

	wantValues := map[string]float64{
		"db_keys_total":          float64(len(keys) + len(keysExpiring)),
		"db_expiring_keys_total": float64(len(keysExpiring)),
	}

	for s := range scrapes {
		if wantVal, ok := wantValues[s.Name]; ok {
			if dbNumStrFull == s.DB && wantVal != s.Value {
				t.Errorf("values not matching, %f != %f", wantVal, s.Value)
			}
		}
	}
}

func init() {
	for _, n := range []string{"john", "paul", "ringo", "george"} {
		key := fmt.Sprintf("key-%s-%d", n, ts)
		keys = append(keys, key)
	}

	for _, n := range []string{"A.J.", "Howie", "Nick", "Kevin", "Brian"} {
		key := fmt.Sprintf("key-exp-%s-%d", n, ts)
		keysExpiring = append(keysExpiring, key)
	}

	redisAddr := flag.String("redis.addr", "localhost:6379", "Address of one or more redis nodes, separated by separator")

	flag.Parse()
	addrs := strings.Split(*redisAddr, ",")
	if len(addrs) == 0 || len(addrs[0]) == 0 {
		log.Fatal("Invalid parameter --redis.addr")
	}
	log.Printf("Using redis addrs: %#v", addrs)
	r = RedisHost{Addrs: addrs}
}
