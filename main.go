package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/opensourceways/community-robot-lib/giteeclient"
	"github.com/opensourceways/community-robot-lib/logrusutil"
	liboptions "github.com/opensourceways/community-robot-lib/options"
	"github.com/opensourceways/community-robot-lib/robot-gitee-framework"
	"github.com/opensourceways/community-robot-lib/secret"
	"github.com/opensourceways/repo-owners-cache/grpc/client"
	"github.com/sirupsen/logrus"
)

type options struct {
	service     liboptions.ServiceOptions
	gitee       liboptions.GiteeOptions
	cacheServer string
}

func (o *options) Validate() error {
	if err := o.service.Validate(); err != nil {
		return err
	}

	if o.cacheServer == "" {
		return fmt.Errorf("cache service address can not be empty")
	}

	return o.gitee.Validate()
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options

	o.gitee.AddFlags(fs)
	o.service.AddFlags(fs)
	fs.StringVar(&o.cacheServer, "cache-server", "", "the cache server address.")

	fs.Parse(args)
	return o
}

func main() {
	logrusutil.ComponentInit(botName)

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.Validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	secretAgent := new(secret.Agent)
	if err := secretAgent.Start([]string{o.gitee.TokenPath}); err != nil {
		logrus.WithError(err).Fatal("Error starting secret agent.")
	}

	defer secretAgent.Stop()

	cacheClient, err := client.NewClient(o.cacheServer)
	if err != nil {
		logrus.WithError(err).Fatal("init cache client fail")
	}

	defer func() {
		if err := cacheClient.Disconnect(); err != nil {
			logrus.WithError(err).Error("disconnect cache server fail")
		}
	}()

	c := giteeclient.NewClient(secretAgent.GetTokenGenerator(o.gitee.TokenPath))

	v, err := c.GetBot()
	if err != nil {
		logrus.WithError(err).Error("Error get bot name")
	}

	r := newRobot(c, cacheClient, v.Login)

	framework.Run(r, o.service)
}
