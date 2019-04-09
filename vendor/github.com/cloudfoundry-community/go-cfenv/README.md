# Go CF Environment Package [![Build Status - Master](https://travis-ci.org/cloudfoundry-community/go-cfenv.svg?branch=master)](https://travis-ci.org/cloudfoundry-community/go-cfenv)

### Overview

[![GoDoc](https://godoc.org/github.com/cloudfoundry-community/go-cfenv?status.png)](https://godoc.org/github.com/cloudfoundry-community/go-cfenv)

`cfenv` is a package to assist you in writing Go apps that run on [Cloud Foundry](http://cloudfoundry.org). It provides convenience functions and structures that map to Cloud Foundry environment variable primitives (http://docs.cloudfoundry.org/devguide/deploy-apps/environment-variable.html).

### Usage

`go get github.com/cloudfoundry-community/go-cfenv`

```go
package main

import (
	"github.com/cloudfoundry-community/go-cfenv"
)

func main() {
	appEnv, _ := cfenv.Current()

	fmt.Println("ID:", appEnv.ID)
	fmt.Println("Index:", appEnv.Index)
	fmt.Println("Name:", appEnv.Name)
	fmt.Println("Host:", appEnv.Host)
	fmt.Println("Port:", appEnv.Port)
	fmt.Println("Version:", appEnv.Version)
	fmt.Println("Home:", appEnv.Home)
	fmt.Println("MemoryLimit:", appEnv.MemoryLimit)
	fmt.Println("WorkingDir:", appEnv.WorkingDir)
	fmt.Println("TempDir:", appEnv.TempDir)
	fmt.Println("User:", appEnv.User)
	fmt.Println("Services:", appEnv.Services)
}
```

### Contributing

Pull requests welcomed.
