package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
	"time"
	"fmt"
	"strconv"
	"strings"
	"github.com/oschwald/geoip2-golang"
)

// Global vars
var SDIR = "/var/db/ix-stats"

// What file to store current stats in
var DAILYFILE string
var MONTHLYFILE string

// Create our mutex we use to prevent race conditions when updating
// counters
var wlock sync.Mutex

// Counter for number of increments before a write
var WCOUNTER = 0

type output_json struct{
	Syscount uint  `json:"systems"`
	Country map[string]float64 `json:"country"`
	Capacity float64 `json:"total_capacity_gb"`
	Disks uint64 `json:"total_disks"`
	Stats map[string]interface{} `json:"stats"`

}
var OUT output_json
var OUT_COUNT map[string]bool
var OUT_MONTH output_json
var OUT_COUNT_MONTH map[string]bool

func convert_to_gigabytes(convert int) int {
	return (convert / 1024 / 1024 / 1024)
}

// Where is this request coming from?
func get_location(clientip string) string {
  //log.Println("Checking IP: " + clientip)
  db, err := geoip2.Open("GeoLite2-Country.mmdb")
  if err != nil { log.Fatal(err) }
  defer db.Close()

  ip := net.ParseIP(clientip)
  record, err := db.Country(ip)
  if err != nil { return "" }
  return record.Country.IsoCode
}

// Getting a new submission
func submit(rw http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)

	// Decode the POST data json struct
	var s map[string]interface{}
	err := decoder.Decode(&s)
	if err != nil {
		log.Println(err)
		return
	}

	// Lookup Geo IP
	//ip,_,_ := net.SplitHostPort(req.RemoteAddr)
	ip := req.Header.Get("X-Forwarded-For")
	isocode := get_location(ip)

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
	//log.Println(OUT)
	// Unlock the mutex now
	wlock.Unlock()
	// Do things with the data
	parseInput(s, isocode)
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
    parseInput(s,"LOCALTEST")
    //raw, _ := json.MarshalIndent(OUT,"","  ")
    //fmt.Println( "Output:", OUT)
    //fmt.Println( string(raw) )
  }
}

func parseInput(inputs map[string]interface{}, geolocation string) {
  //First verify that the system was not already counted
  id := ""
  if tmp, ok := inputs["system_hash"] ; ok {
    id = tmp.(string)
  }
  if id != "" {
    // DAILY STATS OBJECT
    if _, ok:= OUT_COUNT[id] ; !ok {
      OUT_COUNT[id] = true
      //increment the system count
      OUT.Syscount = OUT.Syscount+1
      if len(geolocation)>0 { 
        cnum := OUT.Country[geolocation]
        OUT.Country[geolocation] = cnum+1
      }
      //Now start loading all the input fields and incrementing the counters in the map
      for key := range(inputs) {
        if key=="system_hash" || key=="usage_version" { continue }
        OUT.Stats = addToMap( OUT.Stats, key, inputs[key] )
      }
      OUT = get_storage_totals(OUT, inputs);
    }
    // MONTHLY STATS OBJECT
    if _, ok:= OUT_COUNT_MONTH[id] ; !ok {
      OUT_COUNT_MONTH[id] = true
      //increment the system count
      OUT_MONTH.Syscount = OUT_MONTH.Syscount+1
      if len(geolocation)>0 { 
        cnum := OUT_MONTH.Country[geolocation]
        OUT_MONTH.Country[geolocation] = cnum+1
      }
      //Now start loading all the input fields and incrementing the counters in the map
      for key := range(inputs) {
        if key=="system_hash" || key=="usage_version" { continue }
        OUT_MONTH.Stats = addToMap( OUT_MONTH.Stats, key, inputs[key] )
      }
      OUT_MONTH = get_storage_totals(OUT_MONTH, inputs);
    }

  } //end check for non-empty ID
  
}

func get_storage_totals( OutS output_json, IN map[string]interface{}) output_json {
  // pools -> [] -> (capacity/disks)
  if list, ok := IN["pools"] ; ok {

    for _, obj := range(list.([]interface{})) {
      if val, ok2 := obj.(map[string]interface{})["capacity"] ; ok2 {
        OutS.Capacity += float64( convert_to_gigabytes( int( val.(float64) ) ) );
      }
      if val, ok2 := obj.(map[string]interface{})["disks"] ; ok2 {
        OutS.Disks += uint64(val.(float64));
      }
    }
  }
  return OutS
}

func addToMap( M map[string]interface{}, key string, Val interface{}) map[string]interface{} {
  //fmt.Println("Add To Map", key, Val)
  v := reflect.ValueOf(Val)
  
  if M == nil {
    M = make(map[string]interface{})
  }
  MF := make(map[string]interface{})
  tmp, ok := M[key]
  if ok { MF = tmp.(map[string]interface{}) }

  switch v.Kind(){
  case reflect.Invalid:
      return M

  case reflect.Map:
  	//fmt.Println("Map:", Val)
        sm := Val.(map[string]interface{})
  	for field := range(sm){
  	  MF = addToMap(MF, field, sm[field])
        }

  case reflect.Slice:
	M = addSliceToMap(M, key, Val.([]interface{}) );
        return M

  case reflect.Bool:
	MF = addBoolToMap(MF, Val.(bool))
  case reflect.String:
  	//fmt.Println("String",Val)
  	MF = addStringToMap(MF, Val.(string))

  case reflect.Int, reflect.Int8, reflect.Int32, reflect.Int64:
  	//fmt.Println("INT",Val)
  	MF = addNumberToMap(MF, Val.(int), key)

  case reflect.Uint, reflect.Uint8, reflect.Uint32, reflect.Uint64:
  	//fmt.Println("UINT",Val)
  	MF = addNumberToMap(MF, int( Val.(uint) ), key )

  case reflect.Float32:
  	//fmt.Println("Float32",Val)
  	MF = addNumberToMap(MF, int( Val.(float32)  ), key )

  case reflect.Float64:
  	//fmt.Println("Float64",Val)
  	MF = addNumberToMap(MF, int( Val.(float64)  ), key )

  case reflect.Complex64:
  	//fmt.Println("Complex64",Val)
  	//MF = addNumberToMap(MF, int( Val.(complex64)  ), key )

  case reflect.Complex128:
  	//fmt.Println("Complex128",Val)
  	//MF = addNumberToMap(MF, int( Val.(complex128)  ), key )

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

  } else if (num>=0) {
	out = append(out, val.(string))

  }
  return out
}

func addSliceToMap(M map[string]interface{}, key string, Val []interface{}) map[string]interface{} {
  //Create the optional output map
  MF := make(map[string]interface{})
  tmp, ok := M[key]
  if ok { MF = tmp.(map[string]interface{}) }

  for _, subval := range( Val ) {
    //fmt.Println("subval:", subval)
    _type := reflect.ValueOf(subval).Kind()
    if _type == reflect.Map {
      //List of maps - Need to create a sub-map and add them in there
      submap := subval.(map[string]interface{})

      //fmt.Println("submap:", submap)
      keys := findUniqueKey(submap)
      if len(keys) == 0 {
        //fmt.Println("No Unique Keys", key, submap)
        M = addToMap(M, key, submap)
      } else {
        //fmt.Println("Got Unique Keys", key, keys, submap)
        for _, subKey := range(keys) {
          MF = addToMap(MF, subKey, submap)
        }
      }
    } else {
      //Just a list of strings/numbers/etc - add them directly to the output map
      M = addToMap(M, key, subval)
    }
  } //end loop over elements
  if len(MF) > 0 { M[key] = MF }
  return M;
}

func addNumberToMap(M map[string]interface{}, val int, key string) map[string]interface{} {
  //fmt.Println("Add Number to Map:", val)
  //Check if this number needs to be converted to GB first
  name := strconv.Itoa(val)
  if key=="memory" || key=="capacity" || strings.HasPrefix(key, "usedby") {
    val = convert_to_gigabytes(val);
    name = strconv.Itoa(val)+"GB"
  }
  cnum := 0.0
  if num, err := M[name] ; err { cnum = num.(float64) }
  M[name] = cnum+1
  return M
}

func addStringToMap(M map[string]interface{}, name string) map[string]interface{} {
  //fmt.Println("Add String to Map:", name)
  cnum := 0.0
  if num, err := M[name] ; err { cnum = num.(float64) }
  M[name] = cnum+1
  return M
}

func addBoolToMap(M map[string]interface{}, val bool) map[string]interface{} {
  //fmt.Println("Add String to Map:", name)
  name := "true"
  if !val { name = "false" }
  cnum := 0.0
  if num, err := M[name] ; err { cnum = num.(float64) }
  M[name] = cnum+1
  return M
}

// Clear out the JSON structure counters
func zero_out_stats() {
  OUT = output_json{}
  if OUT.Country == nil {
    OUT.Country = make(map[string]float64)
  }
  OUT_COUNT = make(map[string]bool)
}

func zero_out_monthly_stats() {
  OUT_MONTH = output_json{}
  if OUT_MONTH.Country == nil {
    OUT_MONTH.Country = make(map[string]float64)
  }
  OUT_COUNT_MONTH = make(map[string]bool)
}

// Get the latest daily file to store data
func get_daily_filename() {
  t := time.Now()
  newfile := SDIR + "/" + t.Format("2006-01-02") + ".json"
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
    os.Symlink(DAILYFILE, SDIR+"/latest.json")
  }
  //Now see if we need to rotate the monthly id file as well
  newfile = SDIR+"/"+t.Format("2006-01")+".json"
  if newfile != MONTHLYFILE {
    zero_out_monthly_stats()
    MONTHLYFILE = newfile
    os.Remove(SDIR + "/latest-month.json")
    os.Symlink(MONTHLYFILE, SDIR+"/latest-month.json")
  }

}

// Load the daily file into memory
func load_daily_file() {
  //Verify that the output directory exists
  if _, err := os.Stat(SDIR); os.IsNotExist(err) {
    err = os.MkdirAll(SDIR, 0755)
    if err != nil { fmt.Println("[ERROR] Could not create output directory:", SDIR); os.Exit(1) }
  }
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
  if err := json.Unmarshal(dat, &OUT); err != nil {
    log.Println(err)
    log.Fatal("Failed unmarshal of JSON in DAILYFILE:")
  }
  // Now load the ID file
  dat, err = ioutil.ReadFile(DAILYFILE+".id")
  if err == nil {
    json.Unmarshal(dat, &OUT_COUNT);
  }
}

func load_monthly_file() {
  //Verify that the output directory exists
  if _, err := os.Stat(SDIR); os.IsNotExist(err) {
    err = os.MkdirAll(SDIR, 0755)
    if err != nil { fmt.Println("[ERROR] Could not create output directory:", SDIR); os.Exit(1) }
  }

  // No file yet? Lets clear out the struct
  if _, err := os.Stat(MONTHLYFILE); os.IsNotExist(err) {
    zero_out_monthly_stats()
    return
  }

  // Load the file into memory
  dat, err := ioutil.ReadFile(MONTHLYFILE)
  if err != nil {
    log.Println(err)
    log.Fatal("Failed loading daily file: " + DAILYFILE)
  }
  if err := json.Unmarshal(dat, &OUT_MONTH); err != nil {
    log.Println(err)
    log.Fatal("Failed unmarshal of JSON in DAILYFILE:")
  }
  // Now load the ID file

  dat, err = ioutil.ReadFile(MONTHLYFILE+".id")
  if err == nil {
    json.Unmarshal(dat, &OUT_COUNT_MONTH);
  }  
}

func flush_json_to_disk() {
  fmt.Println("Writing to Files:", DAILYFILE, MONTHLYFILE);
  file, _ := json.MarshalIndent(OUT, "", " ")
  _ = ioutil.WriteFile(DAILYFILE, file, 0644)
  file, _ = json.MarshalIndent(OUT_MONTH, "", " ")
  _ = ioutil.WriteFile(MONTHLYFILE, file, 0644)
  file, _ = json.MarshalIndent(OUT_COUNT, "", " ")
  _ = ioutil.WriteFile(DAILYFILE+".id", file, 0644)
  file, _ = json.MarshalIndent(OUT_COUNT_MONTH, "", " ")
  _ = ioutil.WriteFile(MONTHLYFILE+".id", file, 0644)
  //fmt.Println( string(file))
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

    // Read the current files into memory at startup
    get_daily_filename()
    load_daily_file()
    load_monthly_file()

    // Start our HTTP listener
    http.HandleFunc("/submit", submit)
    log.Fatal(http.ListenAndServe(":8082", nil))

  } else {
    // Dev Test : Loading a list of files directly from the CLI
    //fmt.Println("test")
    // Read the current files into memory at startup
    get_daily_filename()
    load_daily_file()
    load_monthly_file()

    for _, arg := range(os.Args[1:]) {
      readjson(arg)
    }
    flush_json_to_disk()
    //fmt.Println("finished")
  }
}
