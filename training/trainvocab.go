package main

import (
	"os"
	"log"
	"fmt"
	"time"
	"flag"
	"math"
	"errors"
	"regexp"
	"reflect"
	"runtime"
	"math/rand"
	"io/ioutil"
	"sync/atomic"
	"path/filepath"
	"github.com/AlasdairF/Custom"
	"github.com/AlasdairF/Conv"
	"github.com/AlasdairF/Sort/IntInt"
	"github.com/alasdairforsythe/pansearch"
)

var (
	vocabSize int // common: 30000, 30522, 32000, 50265, 65535
	maxTokenLength int // 30
	workers int = runtime.GOMAXPROCS(0) - 1
	strips int = 100
	overlap int = 4
	midwayTarget int = 500000
	datasetFilename string
	dictionaryFilename string
	resultsDir string
	keepTrying int = 500
	reserve256bytes = true
	specialCaps = true

	//var ungreedyPreference = []byte = byte{'‘', '“', '"', '\'', '`', '-', '(', '[', ' ', byte("\r"), byte("\n"), byte("\t")}
	//var ungreedyLookup [256]byte

	remainingTokens_atomic int64
)

type resultStruct struct {
	testVocab *pansearch.KeyBytes
	tokensInText int
	tokensToRemove [][]byte
}

type bestStruct struct {
    tokens    int
    filename  string
}

// Channels that holds the various random dictionaries
var channelWork = make(chan *pansearch.KeyBytes, workers / 2)
var channelResult = make(chan resultStruct, workers * 4)
var regx = regexp.MustCompile("^[0-9]+_[0-9]+\\.[a-zA-Z0-9]+$")

func flagRequired(name string, value interface{}) {
    switch v := reflect.ValueOf(value); v.Kind() {
    case reflect.String:
        if v.String() == "" {
            fmt.Fprintf(os.Stderr, "%s is required\n", name)
            flag.Usage()
            os.Exit(1)
        }
    case reflect.Int:
        if v.Int() == 0 {
            fmt.Fprintf(os.Stderr, "%s is required\n", name)
            flag.Usage()
            os.Exit(1)
        }
    }
}

func formatInt(v int) string {
	return string(conv.FormatThousands(conv.Bytes(v), ','))
}

func save_tokens(filename string, data [][]byte) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()
	w := custom.NewZlibWriter(fi)
	defer w.Close()
	w.WriteUint64(uint64(len(data)))
	for _, b := range data {
		w.WriteBytes8(b)
	}
	return nil
}

func load_saved(filename string) ([][]byte, error) {
	fi, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fi.Close()
	r := custom.NewZlibReader(fi)
	l := int(r.ReadUint64())
	data := make([][]byte, l)
	for i:=0; i<l; i++ {
		data[i] = r.ReadBytes8()
	}
	// Make sure we're at the end
	if r.EOF() != nil {
		return nil, errors.New(filename + ` not valid.`)
	}
	return data, nil
}

func worker(id int, datastrips [][]byte, filedata []byte) {
	var i, index, length, divider, l, remainingTokens, tokensInText, maxlen, missing int
	var run int = 1
	var exists, reachedMidway, found bool
	var data []byte
	scores := make([]sortIntInt.KeyVal, vocabSize)

	for testVocab := range channelWork {
		log.Println(`Worker`, id, `starting run`, run)

		// Reset vars this round's total and scores
		tokensInText = 0
		missing = 0
		for i=0; i<vocabSize; i++ { // Reset scores to index & zero
			scores[i] = sortIntInt.KeyVal{i, 0}
		}

		// Finish building the testVocab
		testVocab.Build()
		maxlen = testVocab.LongestLength() // the longest token length in this testVocab

		// Sanity check, this should never happen
		if testVocab.Len() != vocabSize {
			panic(errors.New(`testVocab contains ` + conv.String(testVocab.Len()) + ` not the target ` + conv.String(vocabSize)))
		}
		
		// If midwayTarget has been reached, check the full dataset
		if !reachedMidway {
			remainingTokens = int(atomic.LoadInt64(&remainingTokens_atomic))
			if remainingTokens <= midwayTarget {
				datastrips[0] = filedata // replace the datastrips with the whole dataset
				datastrips = datastrips[0:1]
				reachedMidway = true
			}
		}

		// This is the main tokenizing loop, but it's duplicated to ensure efficiency
		if reserve256bytes { // we've added all single-character bytes, which means it's impossible to not have a match
			for _, data = range datastrips {
				i = 0
				l = len(data) - maxlen // don't do the last section
				for i < l {
					for length = maxlen; length > 0; length-- {
						_ = data[i:i+length] // Eliminate bounds check
						if index, exists = testVocab.Find(data[i:i+length]); exists {
							scores[index].V += length // this token saved this many characters (its length)
							i += length
							tokensInText++
							break
						}
					}
				}
				// Do the final few characters left at the end
				// This is done separately to avoid checking the length in a loop above
				for length = len(data) - i; length > 0; length-- {
					_ = data[i:i+length] // Eliminate bounds check
					if index, exists = testVocab.Find(data[i:i+length]); exists {
						scores[index].V += length // this token saved this many characters (its length)
						i += length
						tokensInText++
						break
					}
				}
			}
		} else { // without reserve256bytes, it's possible to not match a token, which means I have to check for that
			for _, data = range datastrips {
				i = 0
				l = len(data) - maxlen // don't do the last section
				for i < l {
					found = false
					for length = maxlen; length > 0; length-- {
						_ = data[i:i+length] // Eliminate bounds check
						if index, exists = testVocab.Find(data[i:i+length]); exists {
							scores[index].V += length // this token saved this many characters (its length)
							i += length
							tokensInText++
							found = true
							break
						}
					}
					if !found {
						missing++
						i++
					}
				}
				// Do the final few characters left at the end
				found = false
				for length = len(data) - i; length > 0; length-- {
					_ = data[i:i+length] // Eliminate bounds check
					if index, exists = testVocab.Find(data[i:i+length]); exists {
						scores[index].V += length // this token saved this many characters (its length)
						i += length
						tokensInText++
						found = true
						break
					}
				}
				if !found {
					missing++
					i++
				}
			}
		}

		// What to do if the tokens didn't cover all of the characters?
		// We're just going to act like normal but make the score so bad that this vocabulary will never be chosen
		if missing != 0 {
			tokensInText *= 10
		}

		// Determine tokens to delete
		remainingTokens = int(atomic.LoadInt64(&remainingTokens_atomic))
		keys := testVocab.Keys()
		sortIntInt.Asc(scores) // sort all the tokens by the number of characters they saved (their length * occurences)
		switch {
			case remainingTokens < vocabSize + (vocabSize / 4):
				divider = 2000 	
			case remainingTokens < vocabSize + (vocabSize / 2):
				divider = 1500 	
			case remainingTokens < vocabSize * 2:
				divider = 1000 	
			case remainingTokens < midwayTarget / 6: 	// < 83,333
				divider = 400 								
			case remainingTokens < midwayTarget / 4: 	// < 125,000
				divider = 300 								
			case remainingTokens < midwayTarget / 2: 	// < 250,000
				divider = 200 								
			case remainingTokens < midwayTarget: 		// < 500,000 (below midwayTarget, the entire dataset is used for each run)
				divider = 150 								
			case remainingTokens < (midwayTarget*3)/2: // < 750,000
				divider = 100 								
			case remainingTokens < midwayTarget * 2: 	// < 1,000,000
				divider = 80 								
			case remainingTokens < midwayTarget * 4: 	// < 2,000,000
				divider = 40 								
			case remainingTokens < midwayTarget * 10: 	// < 5,000,000
				divider = 20 							
			default:										
				divider = 10							// 10%
		}
		length = vocabSize / divider
		tokensToRemove := make([][]byte, length)
		index = 0
		for i=0; i<length; i++ {
			if reserve256bytes && len(keys[scores[i].K]) == 1 { // this is a 1 byte token
				length++
				continue
			}
			tokensToRemove[index] = keys[scores[i].K]
			index++
		}
		// Now check if these are still at 0 and if so includes all zeros
		if scores[i].V == 0 {
			for ; i<vocabSize; i++ {
				if scores[i].V > 0 {
					break
				}
				if reserve256bytes && len(keys[scores[i].K]) == 1 { // this is a 1 byte token
					continue
				}
				tokensToRemove = append(tokensToRemove, keys[scores[i].K])
			}
		}
		// Return the result back to the master thread
		channelResult <- resultStruct{testVocab, tokensInText, tokensToRemove}
		log.Println(`Worker`, id, `completed run`, run, ` Tokens:`, formatInt(tokensInText))
		run++
    }
}

func shuffle(original [][]byte) {
	var i, j int
	for i = len(original) - 1; i > 0; i-- {
		j = rand.Intn(i + 1)
		original[i], original[j] = original[j], original[i]
	}
}

// This is a helper function to allow for resuming the progress from a final dictionary
// It returns the score and true if the filename is score_numbers.whatever
func detectSavedFinal(path string) (uint, bool) {
	f := filepath.Base(path)
	if regx.MatchString(f) {
		bs := []byte(f)
		for i, b := range bs {
			if b == '_' {
				return conv.Uint(bs[0:i]), true
			}
		}
	}
	return 0, false
}

func main() {

	flag.IntVar(&maxTokenLength, "max-token-length", maxTokenLength, "the maximum length of a token (required)")
	flag.IntVar(&vocabSize, "vocab", vocabSize, "vocabulary size, e.g. 65535 (required)")
	flag.StringVar(&datasetFilename, "dataset", datasetFilename, "filename of the dataset plain-text (required)")
	flag.StringVar(&dictionaryFilename, "dictionary", dictionaryFilename, "filename of the dictionary generated by makedictionary or any of the saved output files from this app (required)")
	flag.StringVar(&resultsDir, "dir", resultsDir, "The directory to save the results within (required)")
	flag.IntVar(&workers, "workers", workers, "number of worker threads to run, excluding main thread")
	flag.IntVar(&strips, "strips", strips, "number of strips to distribute to the workers")
	flag.IntVar(&overlap, "overlap", overlap, "how much overlap in the dataset given to each worker until midway")
	flag.IntVar(&midwayTarget, "midway-target", midwayTarget, "aggressive until this point, beneath this the full dataset is used for every worker")
	flag.IntVar(&keepTrying, "keep-trying", keepTrying, "program will exit when unable to find a better match this many times in a row")
	flag.BoolVar(&reserve256bytes, "reserve-256", reserve256bytes, "always include 256 tokens representing every single byte [recommended] (default true)")
	flag.Parse()
    flagRequired("max-token-length", maxTokenLength)
    flagRequired("vocab", vocabSize)
    flagRequired("dataset", datasetFilename)
    flagRequired("dictionary", dictionaryFilename)
    flagRequired("dir", resultsDir)

	// Trim trailing slashes from resultsDir and create it if it does not exist
	for len(resultsDir) > 0 && os.IsPathSeparator(resultsDir[len(resultsDir)-1]) {
		resultsDir = resultsDir[:len(resultsDir)-1]
	}
	if _, err := os.Stat(resultsDir); os.IsNotExist(err) {
		os.MkdirAll(resultsDir, 0755)
	}
	resultsDir = resultsDir + string(filepath.Separator)

	// Vars
	rand.Seed(time.Now().UnixNano())
	var i, i2, from, to, remainingTokens, best1percent, uniqueFileNumber, noNewBest, interval10, removed int
	var exists, hasTokensToRemove, reachedMidway, withinVocabX2, reachedVobab bool
	var lastIntervalFileName string
	tokensToRemove := new(pansearch.CounterBytes)
	dictsWithin1percent := make([]bestStruct, 0, 100)
	best := math.MaxInt64
	var vocabSizeEffective = vocabSize
	if reserve256bytes {
		vocabSizeEffective -= 256
	}
	

	// Load all the text
	filedata, err := ioutil.ReadFile(datasetFilename)
    if err != nil {
		panic(err)
    }

	// Distribute the text evenly into strips where each worker has 100 strips of data from throughout the dataset
	// There is a crossover as each strip is double sized
	numstrips := strips * workers
	split := len(filedata) / numstrips
	data := make([][][]byte, workers)
	for i=0; i<workers; i++ {
		data[i] = make([][]byte, strips)
	}
	var on_worker, on_strip int
	for i=0; i<numstrips; i++ {
		from = i * split
		to = (i + overlap) * split
		if len(filedata) < to {
			to = len(filedata)
		}
		data[on_worker][on_strip] = filedata[from:to]
		on_worker++
		if on_worker == workers {
			on_strip++
			on_worker = 0
		}
	}

	// Load the big dictionary of all the tokens from the dataset
	var tokens [][]byte
	tokens, err = load_saved(dictionaryFilename)
	if err != nil {
		panic(err)
	}

	// This section resumes the final run given one of the final run files, it's only here because I needed to do that when testing
	// Usually you would redo the final run from the finalrun file but you can use this to make it continue checking from the be
	if len(tokens) == vocabSize {
		if nscore, is := detectSavedFinal(dictionaryFilename); is {
			best = int(nscore)
			nscore += nscore / 100
			best1percent = int(nscore)
			reachedMidway = true
			withinVocabX2 = true
			reachedVobab = true
			// Recreate dictsWithin1percent from the files in the directory
			uniqueTokens := new(pansearch.CounterBytes)
			for _, b := range tokens {
				if (len(b) > 1) {
					uniqueTokens.Add(b, 1)
				}
			}
			dir := filepath.Dir(dictionaryFilename)
			files, err := ioutil.ReadDir(dir)
			if err != nil {
				panic(err)
			}
			for _, file := range files {
				fpath := filepath.Join(dir, file.Name())
				if nscore2, is := detectSavedFinal(file.Name()); is && nscore2 <= nscore && nscore2 > 0 {
					dictsWithin1percent = append(dictsWithin1percent, bestStruct{int(nscore2), fpath})
					toks, err := load_saved(fpath)
					if err != nil {
						continue
					}
					for _, b := range toks {
						if (len(b) > 1) {
							uniqueTokens.Add(b, 1)
						}
					}
				}
			}
			uniqueTokens.Build()
			tokens = uniqueTokens.Keys() // this is all the tokens that are present in those within 1% of the best score
			log.Println(`Resuming final run from score`, best)
		}
	}
	
	// How many tokens are there?
	remainingTokens = len(tokens)
	remainingTokens_atomic = int64(remainingTokens) // still single-threaded here

	// Launch the worker threads
	for i=0; i<workers; i++ {
		go worker(i, data[i], filedata)
	}

	// Master loop
	for {
		select {
		case result, ok := <- channelResult: // this channel delivers the results
			if !ok { // channel is closed
				break
			}

			// Save all dictionaries within 10% of the best performing one
			if withinVocabX2 { // if we're within 2x the vocabSize
				if result.tokensInText < best {
					best = result.tokensInText
					best1percent = best + (best / 100)
					noNewBest = 0
					log.Println(`New best score`, best)
					i = 0
					for _, v := range dictsWithin1percent {
						if v.tokens > best1percent {
							os.Remove(v.filename)
						} else {
							dictsWithin1percent[i] = v
							i++
						}
					}
					dictsWithin1percent = dictsWithin1percent[0:i]
				} else {
					noNewBest++
				}
				if result.tokensInText < best1percent {
					filename := resultsDir + conv.String(result.tokensInText) + "_" + conv.String(uniqueFileNumber) + ".zlib"
					uniqueFileNumber++
					err = save_tokens(filename, result.testVocab.Keys())
					dictsWithin1percent = append(dictsWithin1percent, bestStruct{result.tokensInText, filename})
				} else if reachedVobab {
					if len(result.tokensToRemove) > 1 { // just remove 2 tokens at a time
						tokensToRemove.Add(result.tokensToRemove[0], 1)
						tokensToRemove.Add(result.tokensToRemove[1], 1)
						hasTokensToRemove = true
					}
				}
			}

			if reachedVobab {
				if noNewBest >= keepTrying {
					log.Println(`-- Exiting --`)
					fmt.Println(`No new best score in`, noNewBest, `runs`)
					fmt.Println(`Best result tokenized`, formatInt(len(filedata)), `bytes with`, formatInt(best), `tokens`)
					fmt.Println(`Average`, string(conv.FloatBytes(float64(len(filedata)) / float64(best), 3)), `characters/token`)
					fmt.Println(`Best results:`)
					for _, v := range dictsWithin1percent {
						if v.tokens > best1percent {
							os.Remove(v.filename) // delete everything not in the top 1%
						} else {
							if v.tokens == best {
								fmt.Println(` `, v.filename) // output the filesnames of all those that are the best, which may be more than 1
							}
						}
					}
					os.Exit(0)
				}
			} else { // add tokens to remove
				if best != result.tokensInText {
					for _, v := range result.tokensToRemove {
						tokensToRemove.Add(v, 1)
					}
					hasTokensToRemove = true
				}
			}

		default:
			// no values left in the channel
			if hasTokensToRemove { // if there are any tokens to cull
				tokensToRemove.Build()
				remainingTokens = 0
				removed = 0
				for i=0; i<len(tokens); i++ {
					if _, exists = tokensToRemove.Find(tokens[i]); !exists {
						tokens[remainingTokens] = tokens[i]
						remainingTokens++
					} else {
						removed++
					}
				}
				tokens = tokens[0:remainingTokens]
				atomic.StoreInt64(&remainingTokens_atomic, int64(remainingTokens))
				debugStr := ``
				if reachedMidway {
					debugStr += ` reachedMidway`
				}
				if withinVocabX2 {
					debugStr += ` withinVocabX2`
				}
				if reachedVobab {
					debugStr += ` reachedVobab`
				}
				if noNewBest > 0 {
					debugStr += ` noNewBest ` + formatInt(noNewBest)
				}
				if best < math.MaxInt64 {
					debugStr += ` best ` + formatInt(best)
				}
				log.Println(`Deleted`, formatInt(removed), `of`, formatInt(tokensToRemove.Len()), `tokens; Remaining`, formatInt(remainingTokens), `tokens;`, debugStr)
				if remainingTokens <= midwayTarget && !reachedMidway {
					save_tokens(resultsDir + `midwaypoint_` + conv.String(remainingTokens) + `.zlib`, tokens)
					log.Println(`Reached midwayTarget`)
					reachedMidway = true
				}
				if remainingTokens <= vocabSize * 2 && !withinVocabX2  {
					save_tokens(resultsDir + `doublevocab_` + conv.String(remainingTokens) + `.zlib`, tokens)
					log.Println(`Reached 2x vocabSize`)
					withinVocabX2 = true
				}
				if remainingTokens < vocabSizeEffective { // its okay to do this multiple times
					log.Println(`Reached vocabSize`)
					if !reachedVobab { // only reset noNewBest the first time
						noNewBest = 0
					}
					// Now make the the final tokens, from all the tokens that are present in all tokensets that are within 1% of the best score
					uniqueTokens := new(pansearch.CounterBytes)
					for _, v := range dictsWithin1percent {
						toks, err := load_saved(v.filename)
						if err != nil {
							panic(err)
						}
						for _, b := range toks {
							if (len(b) > 1) {
								uniqueTokens.Add(b, 1)
							}
						}
					}
					uniqueTokens.Build()
					tokens = uniqueTokens.Keys() // this is all the tokens that are present in those within 10% of the best score
					if !reachedVobab { // only first time
						save_tokens(resultsDir + `finalrun_` + conv.String(remainingTokens) + `.zlib`, tokens)
					}
					reachedVobab = true
					log.Println(`Determining best combination of`, formatInt(len(tokens)), `tokens`)
				}
				tokensToRemove = new(pansearch.CounterBytes) // empty tokensToRemove for next round
				hasTokensToRemove = false
				// Save the tokens every 10, useful for stopping and starting
				if interval10++; interval10 == 10 {
					if len(lastIntervalFileName) > 0 { // delete the last interval file
						os.Remove(lastIntervalFileName)
					}
					lastIntervalFileName = resultsDir + `interval_` + conv.String(remainingTokens) + `.zlib`
					save_tokens(lastIntervalFileName, tokens) // save interval file
					interval10 = 0
				}
			}
			// Shuffle the dictionary and send it out to the workers
			shuffle(tokens)
			i = 0
			for i2 = 0; i2 < 10; i2++ { // I let it make up to 10 dictionaries out of 1 shuffle because shuffles are expensive
				to = i + vocabSizeEffective
				if to > len(tokens) {
					break
				}
				testVocab := new(pansearch.KeyBytes)
				// Add single "reserved" bytes
				if reserve256bytes {
					for i3:=0; i3<256; i3++ {
						testVocab.AddUnsorted([]byte{byte(i3)})
					}
				}
				for ; i<to; i++ {
					testVocab.AddUnsorted(tokens[i])
				}
				channelWork <- testVocab // send the dictionary to the worker channel
			}
			break
		}
	}
}
