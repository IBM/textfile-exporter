package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
    "github.com/prometheus/common/model"

)

func fatal(err error) {
	if err != nil {
		log.Fatalln(err)
	}
}

// This is going to parse the file at the passed path.
func parseMF(path string) (map[string]*dto.MetricFamily, error) {

	// Standard (overkill?) path sanification.
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		path = filepath.Clean(string(os.PathSeparator) + path)
		path, _ = filepath.Rel(string(os.PathSeparator), path)
	}
	path = filepath.Clean(path)

	// We open the path.
	reader, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	// We parse the content to return the metrics family result.
	parser := expfmt.NewTextParser(model.UTF8Validation)
	mf, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return nil, err
	}
	return mf, nil
}

func isOlderThanTwoHours(t time.Time) bool {
	return time.Now().Sub(t) > 2*time.Hour
}

// Main function here.
func main() {

	// Handle passed or default options.
	optListenPort := flag.Int("l", 9014, "listen port")
	optPromPath := flag.String("p", ".", "path for prom file or dir of *.prom files")
	optScanInterval := flag.Duration("i", 30*time.Second, "scan interval")
	optMemoryMaxAge := flag.Duration("m", 25*time.Hour, "max age of in memory metrics")
	optOldFilesAge := flag.Duration("o", 6*time.Hour, "min age of files considered old")
	optOldFilesExternalCmd := flag.String("x", "ls -l {}", "external command executed on old files")


	flag.Usage = func() {
		flag.PrintDefaults()
	}

	flag.Parse()

	// Create our collector.
	collector := newTimeAwareCollector(*optMemoryMaxAge)

	// Start a background job to constantly watch for files and parse them.
	go func() {
		log.Printf("Textfile Exporter started\n")
		for { // for ever
			filepath := *optPromPath
			fileinfo, err := os.Stat(filepath)
			if err != nil {
				break
			}
			var debugging bool

			// We have a simple runtime-switchable debug option.
			// If this file exists and is not older then two hours, debug
			// output is enabled.
			if fs, err := os.Stat(filepath + "/debug_tfe"); err == nil {
				if !isOlderThanTwoHours(fs.ModTime()) {
					debugging = true
				} else {
					debugging = false
				}
			} else {
				debugging = false
			}
			if debugging {
				log.Printf("*** DEBUG MODE ENABLED ***\n")
			}

			// Let's collect a list of files to process.
			var files []string
			// For a dir, we process the contained files named "*.prom".
			if fileinfo.IsDir() {
				entries, err := os.ReadDir(filepath)
				if err != nil {
					break
				}
				for _, entry := range entries {
					fi, err := entry.Info()
					if err != nil {
						continue
					}
					if fi.IsDir() {
						continue // We do not do recursion.
					}
					name := fi.Name()
					if fi.Mode().IsRegular() && strings.HasSuffix(name, ".prom") {
						files = append(files, filepath+"/"+fi.Name())
					}
				}
			} else {
				files = append(files, filepath)
			}
			n := len(files)
			log.Printf("Found %d files\n", n)

			// Make the collector empty before parsing the files.
			// This creates a possibility to have a totally/partially
			// populated collector if a collect happens before
			// we complete the parsing. Improvement area here.
			collector.Clear()

			// Parse the files.
			for i, f := range files {
				printIt := debugging || i < 5 || i >= n-5
				if printIt {
					log.Printf("%d/%d Processing file %s\n", i+1, n, f)
				}
				// check age
				fileinfo, err := os.Stat(f)
				if err != nil {
					log.Printf("%d/%d Error stat()ing file %s\n", i+1, n, f)
					continue
				}
				// Old files are ignored and a specified external script may be run.
				if time.Now().After(fileinfo.ModTime().Add(*optOldFilesAge)) {
					log.Printf("%d/%d Old file %s\n", i+1, n, f)
					cmdString := strings.ReplaceAll(*optOldFilesExternalCmd, "{}", f)
					cmd := exec.Command("sh", "-c", cmdString) /* #nosec G204 */ // External execution as designed.
					log.Printf("%d/%d Running command %s\n", i+1, n, cmdString)
					cmdOut, err := cmd.Output()
					if err != nil {
						log.Printf("%d/%d Error running command %s\n", i+1, n, cmdString)
					}
					fmt.Println("output:\n<<<\n" + string(cmdOut) + ">>>")
					continue
				}
				// Actual parsing.
				mfs, err := parseMF(f)
				if err != nil {
					log.Printf("%d/%d Error parsing file %s\n", i+1, n, f)
					continue
				}

				// Handle parsing results.
				cnt := 0
				for name, mf := range mfs {
					labels := make(map[string]string)
					if debugging {
						log.Println("Metric Name: ", name)
						log.Println("Metric Type: ", mf.GetType())
						log.Println("Metric Help: ", mf.GetHelp())
					}

					var metric_value float64
					var metric_type prometheus.ValueType
				out:
					for _, m := range mf.GetMetric() {
						switch mf.GetType() {
						case dto.MetricType_GAUGE:
							metric_type = prometheus.GaugeValue
							metric_value = m.GetGauge().GetValue()
						case dto.MetricType_COUNTER:
							metric_type = prometheus.CounterValue
							metric_value = m.GetCounter().GetValue()
						case dto.MetricType_SUMMARY:
							break out
						case dto.MetricType_UNTYPED:
							metric_type = prometheus.UntypedValue
							metric_value = m.GetUntyped().GetValue()
						case dto.MetricType_HISTOGRAM:
							break out
						default:
							break out
						}

						// Handle the timestamp.
						timestamp := m.GetTimestampMs()
						if debugging {
							log.Println("  Metric Value: ", metric_value)
							log.Println("  Timestamp: ", timestamp)
						}
						if timestamp <= 0 { // We generate a timestamp if it is missing.
							timestamp = time.Now().UTC().UnixNano() / 1000000
							if debugging {
								log.Println("  Timestamp: ", timestamp, " (now)")
							}
						}

						// Handle the labels.
						for _, label := range m.GetLabel() {
							if debugging {
								log.Println("  Label_Name:  ", label.GetName())
								log.Println("  Label_Value: ", label.GetValue())
							}
							labels[label.GetName()] = label.GetValue()
						}

						// Add the metric into the collector.
						collector.Add(name, labels, metric_type, metric_value, time.Unix(0, timestamp*int64(time.Millisecond)), 0, mf.GetHelp())
						cnt++

						if debugging {
							log.Println("-----------")
						}
					}
				}
				if printIt {
					log.Printf("%d/%d    found %d data points\n", i+1, n, cnt)
				}
			}
			time.Sleep(*optScanInterval)

		}

	}()

	// Register ourselves.
	r := prometheus.NewRegistry()
	r.MustRegister(collector)
	handler := promhttp.HandlerFor(r, promhttp.HandlerOpts{})
	http.Handle("/metrics", handler)
	http.HandleFunc("/alive", aliveAnswer)

	// Configure the http server and start it.
	s := &http.Server{
		Addr:           ":" + strconv.Itoa(*optListenPort),
		Handler:        nil,
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	log.Fatal(s.ListenAndServe())
}

// This can be called by liveness probes, a lot better than
// invoking the /metrics endpoint and generate output that
// will be ignored.
func aliveAnswer(w http.ResponseWriter, req *http.Request) {
	log.Println("confirming i'm alive")
	fmt.Fprintf(w, "i'm alive\n")
}
