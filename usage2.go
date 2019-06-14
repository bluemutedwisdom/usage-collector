package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	//"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
	"time"
	"fmt"
	"strconv"
	//"github.com/oschwald/geoip2-golang"
)

// Global vars
var SDIR = "/var/db/ix-stats"

// What file to store current stats in
var DAILYFILE string

// Create our mutex we use to prevent race conditions when updating
// counters
var wlock sync.Mutex

// Counter for number of increments before a write
var WCOUNTER = 0

type output_json struct{
	Syscount uint  `json:"systems"`
	Stats map[string]interface{} `json:"stats"`

}

/*func parse_data(s submission_json, isocode string) {

	// Do this all within a locked mutex
	wlock.Lock()

	// Check if the daily file needs to roll over
	get_daily_filename()

	// Every 20 updates, we update the JSON file on disk
	if WCOUNTER >= 20 {
		//log.Println("Flushing to disk")
		flush_json_to_disk()
		WCOUNTER = 0
	} else {
		WCOUNTER++
	}

	//log.Println(TJSON)

	// Unlock the mutex now
	wlock.Unlock()
}*/

func convert_to_gigabytes(convert uint) uint {
	return (convert / 1024 / 1024 / 1024)
}

// Where is this request coming from?
/*func get_location(clientip string) string {
	//log.Println("Checking IP: " + clientip)

	db, err := geoip2.Open("GeoLite2-Country.mmdb")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	ip := net.ParseIP(clientip)
	record, err := db.Country(ip)
	if err != nil {
		return ""
	}
	return record.Country.IsoCode
}*/

// Getting a new submission
func submit(rw http.ResponseWriter, req *http.Request) {
	//decoder := json.NewDecoder(req.Body)

	// Decode the POST data json struct
	/*var s submission_json
	err := decoder.Decode(&s)
	if err != nil {
		log.Println(err)
		return
	}

	// Lookup Geo IP
	//ip,_,_ := net.SplitHostPort(req.RemoteAddr)
	ip := req.Header.Get("X-Forwarded-For")
	isocode := get_location(ip)

	// Do things with the data
	parse_data(s, isocode)*/
}

func readjson( path string ) {
   jsfile, err := os.Open(path)
   if err == nil {
    _data, _ := ioutil.ReadAll(jsfile);
    var s map[string]interface{}

    json.Unmarshal(_data, &s)
    jsfile.Close()
    //fmt.Println(_data)
    //fmt.Println("Input:", s)
    o := parseInput(s)
    raw, _ := json.MarshalIndent(o,"","  ")
    //fmt.Println( "Output:", o)
    fmt.Println( " - JSON:", string(raw) )
  }
}

func parseInput(inputs map[string]interface{}) output_json {
  var out output_json
  //increment the system count
  out.Syscount = out.Syscount+1
  //Now start loading all the input fields and incrementing the counters in the map
  for key := range(inputs) {
    out.Stats = addToMap( out.Stats, key, inputs[key] )
  }
  return out
}

func addToMap( M map[string]interface{}, key string, Val interface{}) map[string]interface{} {
  //fmt.Println("Add To Map")
  v := reflect.ValueOf(Val)
  
  if M == nil {
    M = make(map[string]interface{})
  }
  MF := make(map[string]interface{})
  tmp, ok := M[key]
  if ok { MF = tmp.(map[string]interface{}) }

  switch v.Kind(){
  case reflect.Invalid:
	return M //nothing to do

  case reflect.Map:
  	//fmt.Println("Map:", Val)
        sm := Val.(map[string]interface{})
  	for field := range(sm){
  	  MF = addToMap(MF, field, sm[field])
        }

  case reflect.Slice:
  	//fmt.Println("Slice", Val)
	for _, subval := range( Val.([]interface{}) ) {
	  //fmt.Println("subval:", subval)
	  _type := reflect.ValueOf(subval).Kind()
	  if _type == reflect.Map {
            submap := subval.(map[string]interface{})
	    //fmt.Println("submap:", submap)
	    keys := findUniqueKey(submap)
	    if len(keys) == 0 {
	      fmt.Println("No Unique Keys", key, submap)
	      M = addToMap(M, key, submap)
	    } else {
              fmt.Println("Got Unique Keys", key, keys, submap)
	      for _, subKey := range(keys) {
	        MF = addToMap(MF, subKey, submap)
	      }
	      M[key] = MF
	    }
	  } else {
	    M = addToMap(M, key, subval)
	  }
        }
        return M

  case reflect.Bool:
	MF = addBoolToMap(MF, Val.(bool))
  case reflect.String:
  	//fmt.Println("String",Val)
  	MF = addStringToMap(MF, Val.(string))

  case reflect.Int, reflect.Int8, reflect.Int32, reflect.Int64:
  	//fmt.Println("INT",Val)
  	MF = addNumberToMap(MF, Val.(int))

  case reflect.Uint, reflect.Uint8, reflect.Uint32, reflect.Uint64:
  	//fmt.Println("UINT",Val)
  	MF = addNumberToMap(MF, int( Val.(uint) ) )

  case reflect.Float32:
  	//fmt.Println("Float32",Val)
  	MF = addNumberToMap(MF, int( Val.(float32)  ) )

  case reflect.Float64:
  	//fmt.Println("Float64",Val)
  	MF = addNumberToMap(MF, int( Val.(float64)  ) )

  case reflect.Complex64:
  	//fmt.Println("Complex64",Val)
  	//MF = addNumberToMap(MF, int( Val.(complex64)  ) )

  case reflect.Complex128:
  	//fmt.Println("Complex128",Val)
  	//MF = addNumberToMap(MF, int( Val.(complex128)  ) )

  default:
  	fmt.Println("Default",Val, v.Kind())
  }
  if len(MF) == 0 { fmt.Println("[UNKNOWN]", key, Val) }
  M[key] = MF
  return M
}

func findUniqueKey( M map[string]interface{}) []string {
  priority := []string{"name","release", "members", "type"}
  val, ok := M[priority[0]]
  num := 0
  for !ok && (num < 3) {
	num = num+1
	val, ok = M[priority[num]]
  }
  var out []string
  if !ok {
    return out 
  } else if (num == 2) {
    //This is a slice of keys
    for _, i := range(val.([]interface{})) { out = append(out, i.(string)) }

  } else if (num>0) {
	out = append(out, val.(string))

  }
  return out
}

func addNumberToMap(M map[string]interface{}, val int) map[string]interface{} {
  //fmt.Println("Add Number to Map:", val)
  name := strconv.Itoa(val)
  cnum := 0
  if num, err := M[name] ; err { cnum = num.(int) }
  M[name] = cnum+1
  return M
}

func addStringToMap(M map[string]interface{}, name string) map[string]interface{} {
  //fmt.Println("Add String to Map:", name)
  cnum := 0
  if num, err := M[name] ; err { cnum = num.(int) }
  M[name] = cnum+1
  return M
}

func addBoolToMap(M map[string]interface{}, val bool) map[string]interface{} {
  //fmt.Println("Add String to Map:", name)
  name := "true"
  if !val { name = "false" }
  cnum := 0
  if num, err := M[name] ; err { cnum = num.(int) }
  M[name] = cnum+1
  return M
}

/*func addSliceToMap(M map[string]interface{}, val )
{
	mapslice := M[val]
}*/

// Clear out the JSON structure counters
func zero_out_stats() {
	//TJSON = tracking_json{}
}

// Get the latest daily file to store data
func get_daily_filename() {
	t := time.Now()
	newfile := SDIR + "/" + t.Format("20060102") + ".json"
	if newfile != DAILYFILE {

		// Flush previous data to disk
		if DAILYFILE != "" {
			flush_json_to_disk()
		}
		// Timestamp has changed, lets reset our in-memory json counters structure
		zero_out_stats()

		// Set new DAILYFILE
		DAILYFILE = newfile

		// Update the latest.json symlink
		os.Remove(SDIR + "/latest.json")
		os.Symlink(newfile, SDIR+"/latest.json")
	}

}

// Load the daily file into memory
func load_daily_file() {
	/*get_daily_filename()

	// No file yet? Lets clear out the struct
	if _, err := os.Stat(DAILYFILE); os.IsNotExist(err) {
		zero_out_stats()
		return
	}

	// Load the file into memory
	dat, err := ioutil.ReadFile(DAILYFILE)
	if err != nil {
		log.Println(err)
		log.Fatal("Failed loading daily file: " + DAILYFILE)
	}
	if err := json.Unmarshal(dat, &TJSON); err != nil {
		log.Println(err)
		log.Fatal("Failed unmarshal of JSON in DAILYFILE:")
	}*/
}

func flush_json_to_disk() {
	/*file, _ := json.MarshalIndent(TJSON, "", " ")
	_ = ioutil.WriteFile(DAILYFILE, file, 0644)*/
}

// Lets do it!
func main() {
    if len(os.Args) < 2 {
	// Capture SIGTERM and flush JSON to disk
	var gracefulStop = make(chan os.Signal)
	signal.Notify(gracefulStop, syscall.SIGTERM)
	signal.Notify(gracefulStop, syscall.SIGINT)
	go func() {
		sig := <-gracefulStop
		log.Println("%v", sig)
		log.Println("Caught Signal. Flushing JSON to disk")
		flush_json_to_disk()
		os.Exit(0)
	}()

	// Read the daily file into memory at startup
	load_daily_file()

	// Start our HTTP listener
	http.HandleFunc("/submit", submit)
	log.Fatal(http.ListenAndServe(":8082", nil))
	} else {
		 fmt.Println("test")
		load_daily_file()
    	readjson(os.Args[1])
    	fmt.Println("finished")
	}
}