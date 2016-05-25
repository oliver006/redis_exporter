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
	keys = []string{}
	ts   = int32(time.Now().Unix())
	r    = RedisHost{}
)

func addKeysToDB(t *testing.T, db string) error {

	c, err := redis.Dial("tcp", r.Addrs[0])
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("SELECT", db)
	if err != nil {
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	for _, key := range keys {
		_, err = c.Do("SET", key, "123")
		if err != nil {
			t.Errorf("couldn't setup redis, err: %s ", err)
			return err
		}
	}

	return nil
}

func deleteKeysFromDB(t *testing.T, db string) error {

	c, err := redis.Dial("tcp", r.Addrs[0])
	if err != nil {
		log.Printf("redis err: %s", err)
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}
	defer c.Close()

	_, err = c.Do("SELECT", db)
	if err != nil {
		log.Printf("redis err: %s", err)
		t.Errorf("couldn't setup redis, err: %s ", err)
		return err
	}

	for _, key := range keys {
		c.Do("DEL", key)
	}

	return nil
}

func TestCountingKeys(t *testing.T) {

	e := NewRedisExporter(r, "test")

	scrapes := make(chan scrapeResult)
	go e.scrape(scrapes)

	var keysDB11 float64
	for s := range scrapes {
		//	log.Printf("NAME: ", s.Name)
		if s.Name == "db_keys_total" && s.DB == "db11" {
			keysDB11 = s.Value
			break
		}
	}

	addKeysToDB(t, "11")
	defer deleteKeysFromDB(t, "11")
	scrapes = make(chan scrapeResult)
	go e.scrape(scrapes)

	want := keysDB11 + float64(len(keys))

	for s := range scrapes {
		// log.Printf("xxx: %s  %s   %f ", s.Name, s.DB, s.Value)
		if s.Name == "db_keys_total" && s.DB == "db11" {
			if want != s.Value {
				t.Errorf("values not matching, %f != %f", keysDB11, s.Value)
			}
			break
		}
	}

	deleteKeysFromDB(t, "11")
	scrapes = make(chan scrapeResult)
	go e.scrape(scrapes)

	for s := range scrapes {
		if s.Name == "db_keys_total" && s.DB == "db11" {
			if keysDB11 != s.Value {
				t.Errorf("values not matching, %f != %f", keysDB11, s.Value)
			}
			break
		}
	}
}

func TestExporter(t *testing.T) {

	e := NewRedisExporter(r, "test")

	scrapes := make(chan scrapeResult)
	go e.scrape(scrapes)

	addKeysToDB(t, "11")
	defer deleteKeysFromDB(t, "11")

	e.setMetrics(scrapes)

	want := 20
	if len(e.metrics) < want {
		t.Errorf("need moar metrics, found %d, want > %d", len(e.metrics), want)
	}

	wantKeys := []string{"db_keys_total", "instantaneous_ops_per_sec", "used_cpu_sys"}

	for _, k := range wantKeys {
		if _, ok := e.metrics[k]; !ok {
			t.Errorf("missing metrics key: %s", k)
		}
	}
}

func init() {
	for _, n := range []string{"john", "paul", "ringo", "george"} {
		key := fmt.Sprintf("key-%s-%d", n, ts)
		keys = append(keys, key)
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
