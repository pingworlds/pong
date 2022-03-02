package main

import (
	"flag"
	"io/ioutil"
	"log" 
	"os"

	"github.com/pingworlds/pong/config"
	"github.com/pingworlds/pong/service"
)

func main() {
	var mode, cfgName, dir string
	flag.StringVar(&mode, "m", "remote", "run mode:local or remote")
	flag.StringVar(&dir, "d", "./", "work dir",)
	flag.StringVar(&cfgName, "c", "", "config file")

	flag.Parse()

	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			log.Fatalln(err)
			return
		}
	}

	if cfgName == "" {
		if mode == "remote" {
			cfgName = dir + "/remote.json"
		} else {
			cfgName = dir + "/local.json"
		}
	}
	loadCofig(cfgName)
	var cfg = config.Config
	if cfg.WorkDir == "" {
		config.Config.WorkDir = dir
	}

	if mode == "local" {
		service.StartLocal()
	} else if mode == "remote" {
		service.StartRemote()
	} else {
		log.Println("error unkown mode")
	}

}

func loadCofig(path string) (err error) {
	file, err := os.Open(path)
	if err != nil {
		log.Fatalln(err)
		return
	}
	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		log.Fatalln(err)
		return
	}

	service.SetConfig(bytes)
	return
}
