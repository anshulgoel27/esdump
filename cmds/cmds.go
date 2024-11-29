package cmds

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"github.com/olivere/elastic/v7"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "elasticsearch export",
	Long:  `elasticsearch export`,
	Run: func(cmd *cobra.Command, args []string) {
		log.Printf("export index %s to %s", IndexName, Output)
		ExportData(Output, EsUrl, IndexName, MatchBody)
	},
}

var Output string
var MaxDocs int
var MatchBody string

var Input string
var enableGzip bool

func init() {
	exportCmd.Flags().StringVarP(&Output, "o", "o", "./tmp_export.json.gz", "export dest filename; use - for stdout")
	exportCmd.Flags().IntVarP(&MaxDocs, "c", "c", 0, "set the max amount of documents to be exported; default(0) will exported all matched document; ")
	exportCmd.Flags().StringVarP(&MatchBody, "MatchBody", "m", "{\"match_all\":{}}", "MatchBody, empty for match_all; example:{\"range\": {\"timestamp\": {\"gte\": \"2021-04-20\"}}}")
	exportCmd.Flags().BoolVar(&enableGzip, "gzip", true, "enable gzip; to disable gzip add parameter \"--gzip=false\"")

	RootCmd.AddCommand(exportCmd)
}

func ExportData(outputFile, esUrl, indexName, matchBody string) (err error) {
	var ofile *os.File
	if outputFile == "-" {
		ofile = os.Stdout
	} else {
		if enableGzip && !strings.HasSuffix(outputFile, ".gz") {
			outputFile += ".gz"
		}
		ofile, err = os.OpenFile(outputFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	}
	defer ofile.Close()

	if err != nil {
		log.Print("open file err", err)
		return err
	}
	var targetWriter io.Writer
	if enableGzip {
		zip := gzip.NewWriter(ofile)
		defer zip.Flush()
		defer zip.Close()
		targetWriter = zip
	} else {
		targetWriter = ofile
	}

	outputWriter := bufio.NewWriterSize(targetWriter, 1<<22)
	defer outputWriter.Flush()
	ss := GetEsScrollService(esUrl, indexName)
	if matchBody != "" {
		rawQuery := elastic.NewRawStringQuery(matchBody)
		ss = ss.Query(rawQuery)
		log.Println("export match:", matchBody)
	}
	pager := ss.Size(10000) //.Query(elastic.MatchAllQuery{})
	pcounter := 0
	count := 0
	dataChan := make(chan interface{}, 300)
	fetchTime := 0.0
	totalFetchTime := 0.0
	go func() {
		defer close(dataChan)
		for {
			//for test
			startTime := time.Now()
			res, err := pager.Do(context.Background())
			spend := time.Since(startTime).Seconds()
			fetchTime += spend
			totalFetchTime += spend
			pcounter++
			if pcounter%10000 == 0 {
				log.Println("10000 pages FetchTime", fetchTime, "s")
				fetchTime = 0
			}
			if err == nil {
				for _, hit := range res.Hits.Hits {
					dataChan <- *hit
					count++
					if MaxDocs > 0 && count >= MaxDocs {
						goto END
					}
				}
				if len(res.Hits.Hits) < 100 {
					goto END
				}
			}
			if err != nil {
				log.Fatalln("ScrollService err", err)
			}
		}
	END:
		log.Println("totalFetchTime", totalFetchTime, "s")
	}()

	storeTime := 0.0
	bsCounter := 0
	storeCount := 0
	for chanItem := range dataChan {
		storeCount += 1
		hit := chanItem.(elastic.SearchHit)
		//item := hitItem{hit.Id, hit.Source}
		bs, _ := json.Marshal(&hit)
		_, err = outputWriter.Write(bs)
		_, err = outputWriter.Write([]byte("\n"))
		if err != nil {
			log.Println("io err:", err)
			break
		}
		bsCounter += len(bs)
		if storeCount%10000 == 0 {
			log.Printf("total exported %d items; total_raw_bytes: %.2f MB; storeTime %f", storeCount, getMb(int64(bsCounter)), storeTime)
			storeTime = 0
		}
	}
	if err != nil {
		log.Print(err)
	}
	if enableGzip {
		stat, _ := ofile.Stat()
		fsize := getMb(stat.Size())
		log.Printf("total exported %d items; total_raw_bytes: %.2f MB;the gzip size: %.2f MB", storeCount, getMb(int64(bsCounter)), fsize)
	} else {
		log.Printf("total exported %d items; total_raw_bytes: %.2f MB; storeTime %f", storeCount, getMb(int64(bsCounter)), storeTime)
	}
	return err
}
func getMb(size int64) float64 {
	tmpf := float64(size) / (1024 * 1024) * 100
	tmpf = math.Trunc(tmpf) / 100
	return tmpf
}
