/*
Copyright 2018 Heptio Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package worker

import (
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/heptio/sonobuoy/pkg/plugin"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

func init() {
	mime.AddExtensionType(".gz", "application/gzip")
}

// GatherResults is the consumer of a co-scheduled container that agrees on the following
// contract:
//
// 1. Output data will be placed into an agreed upon results directory.
// 2. The Job will wait for a done file
// 3. The done file contains a single string of the results to be sent to the master
func GatherResults(waitfile string, url string, client *http.Client) error {
	logrus.WithField("waitfile", waitfile).Info("Waiting for waitfile")
	signals := sigHandler()
	ticker := time.Tick(1 * time.Second)
	stop := make(chan struct{}, 1)
	// TODO(chuckha) evaluate wait.Until [https://github.com/kubernetes/apimachinery/blob/e9ff529c66f83aeac6dff90f11ea0c5b7c4d626a/pkg/util/wait/wait.go]
	for {
		select {
		case <-ticker:
			if resultFile, err := ioutil.ReadFile(waitfile); err == nil {
				logrus.WithField("resultFile", string(resultFile)).Info("Detected done file, transmitting result file")
				return handleWaitFile(string(resultFile), url, client)
			}
		case <-signals:
			// Run a goroutine here so we can keep checking the done file before cleaning up.
			go func() {
				time.Sleep(plugin.GracefulShutdownPeriod)
				stop <- struct{}{}
			}()
		case <-stop:
			logrus.Info("Did not receive plugin results in time. Shutting down worker.")
			close(stop)
			return nil
		}
	}
}

func handleWaitFile(resultFile, url string, client *http.Client) error {
	var outfile *os.File
	var err error

	// Set content type
	extension := filepath.Ext(resultFile)
	mimeType := mime.TypeByExtension(extension)

	defer func() {
		if outfile != nil {
			outfile.Close()
		}
	}()

	// transmit back the results file.
	return DoRequest(url, client, func() (io.Reader, string, error) {
		outfile, err = os.Open(resultFile)
		return outfile, mimeType, errors.WithStack(err)
	})
}

// sigHandler is used to manage graceful cleanups when a TERM signal is received.
func sigHandler() <-chan struct{} {
	stop := make(chan struct{})
	go func() {
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, syscall.SIGTERM)
		sig := <-sigc
		logrus.WithField("signal", sig).Info("got a signal, waiting then sending the real shutdown signal")
	}()
	return stop
}
