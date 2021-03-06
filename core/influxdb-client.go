package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	dguard "github.com/90TechSAS/libgo-docker-guard"
	influxdb "github.com/influxdb/influxdb/client"

	"../utils"
)

/*
	Container's stats
*/
type Stat struct {
	ContainerID   string
	Time          time.Time
	SizeRootFs    float64
	SizeRw        float64
	SizeMemory    float64
	NetBandwithRX float64
	NetBandwithTX float64
	CPUUsage      float64
	Running       bool
}

/*
	HTTP GET options
*/
type Options struct {
	Since  string
	Before string
	Limit  int
}

const (
	StatsMeasurements = "cstats"
)

/*
	Client variables
*/
var (
	// DB
	DB *influxdb.Client
)

/*
	Initialize InfluxDB connection
*/
func InitDB() {
	var err error

	// Parse InfluxDB server URL
	u, err := url.Parse(fmt.Sprintf("http://%s:%d", DGConfig.DockerGuard.InfluxDB.IP, DGConfig.DockerGuard.InfluxDB.Port))
	if err != nil {
		l.Critical("Can't parse InfluxDB config :", err)
	}

	// Make InfluxDB config
	conf := influxdb.Config{
		URL:      *u,
		Username: os.Getenv("INFLUX_USER"),
		Password: os.Getenv("INFLUX_PWD"),
	}

	// Connect to InfluxDB server
	DB, err = influxdb.NewClient(conf)
	if err != nil {
		l.Critical("Can't connect to InfluxDB:", err)
	}

	// Test InfluxDB server connectivity
	dur, ver, err := DB.Ping()
	if err != nil {
		l.Critical("Can't ping InfluxDB:", err)
	}
	l.Verbose("Connected to InfluxDB! ping:", dur, "/ version:", ver)

	// Create DB if doesn't exist
	_, err = queryDB(DB, "create database "+DGConfig.DockerGuard.InfluxDB.DB)
	if err != nil {
		if err.Error() != "database already exists" {
			l.Critical("Create DB:", err)
		}
	}
}

/*
	Send a query to InfluxDB server
*/
func queryDB(con *influxdb.Client, cmd string) (res []influxdb.Result, err error) {
	q := influxdb.Query{
		Command:  cmd,
		Database: DGConfig.DockerGuard.InfluxDB.DB,
	}
	if response, err := con.Query(q); err == nil {
		if response.Error() != nil {
			return res, response.Error()
		}
		res = response.Results
	}
	return
}

/*
	Parse Options
*/
func GetOptions(r *http.Request) Options {
	var options Options // Returned options
	var err error       // Error handling

	// Get url parameters
	oS := r.URL.Query().Get("since")
	oB := r.URL.Query().Get("before")
	oL := r.URL.Query().Get("limit")

	// Set options
	options.Since = oS
	options.Before = oB
	oLInt, err := utils.S2I(oL)
	if err != nil {
		options.Limit = -1
	} else {
		options.Limit = oLInt
	}

	return options
}

/*
	Insert a stat
*/
func (s *Stat) Insert(probeName string) error {
	var pts = make([]influxdb.Point, 1) // InfluxDB point
	var err error                       // Error handling

	l.Silly("Insert stat:", s)
	// Make InfluxDB point
	pts[0] = influxdb.Point{
		Measurement: StatsMeasurements,
		Tags: map[string]string{
			"containerid": s.ContainerID,
			"probename":   probeName,
		},
		Fields: map[string]interface{}{
			"sizerootfs":    float64(s.SizeRootFs),
			"sizerw":        float64(s.SizeRw),
			"sizememory":    float64(s.SizeMemory),
			"netbandwithrx": float64(s.NetBandwithRX),
			"netbandwithtx": float64(s.NetBandwithTX),
			"cpuusage":      float64(s.CPUUsage),
			"running":       s.Running,
		},
		Time:      time.Now(),
		Precision: "s",
	}

	// InfluxDB batch points
	bps := influxdb.BatchPoints{
		Points:          pts,
		Database:        DGConfig.DockerGuard.InfluxDB.DB,
		RetentionPolicy: "default",
	}

	// Write point in InfluxDB server
	timer := time.Now()
	l.Warn("Insert:", bps)
	_, err = DB.Write(bps)
	if err != nil {
		l.Error("Failed to write in InfluxDB:", bps, ". Error:", err)
	} else {
		l.Silly("Stat inserted in ", time.Since(timer), ":", bps)
	}

	return err
}

/*
	Insert some stats
*/
func InsertStats(stats []Stat, probeName string) error {
	if len(stats) < 1 {
		return errors.New("len(stats) < 1")
	}

	var pts = make([]influxdb.Point, len(stats)) // InfluxDB point
	var err error                                // Error handling

	l.Silly("Insert stats:", stats)
	// Make InfluxDB points
	for i := 0; i < len(stats); i++ {
		pts[i] = influxdb.Point{
			Measurement: StatsMeasurements,
			Tags: map[string]string{
				"containerid": stats[i].ContainerID,
				"probename":   probeName,
			},
			Fields: map[string]interface{}{
				"sizerootfs":    float64(stats[i].SizeRootFs),
				"sizerw":        float64(stats[i].SizeRw),
				"sizememory":    float64(stats[i].SizeMemory),
				"netbandwithrx": float64(stats[i].NetBandwithRX),
				"netbandwithtx": float64(stats[i].NetBandwithTX),
				"cpuusage":      float64(stats[i].CPUUsage),
				"running":       stats[i].Running,
			},
			Time:      time.Now(),
			Precision: "us",
		}
	}

	// InfluxDB batch points
	bps := influxdb.BatchPoints{
		Points:          pts,
		Database:        DGConfig.DockerGuard.InfluxDB.DB,
		RetentionPolicy: "default",
	}

	// Write points in InfluxDB server
	timer := time.Now()
	_, err = DB.Write(bps)
	if err != nil {
		l.Error("Failed to write in InfluxDB:", bps, ". Error:", err)
	} else {
		l.Silly("Stat inserted in ", time.Since(timer), ":", bps)
	}

	return err
}

/*
	Get container's last stat
*/
func GetLastStat(c *dguard.Container) (Stat, error) {
	var stat Stat // Returned stat
	var err error // Error handling

	query := `	SELECT 	last(cpuusage),
						last(netbandwithrx),
						last(netbandwithtx),
						last(running),
						last(sizememory),
						last(sizerootfs),
						last(sizerw) 
				FROM cstats
				WHERE containerid = '` + c.ID + `' fill(none)`

	// Send query
	res, err := queryDB(DB, query)
	if err != nil {
		return stat, err
	}

	// Get results
	for _, row := range res[0].Series[0].Values {
		var statValues [8]float64
		if len(row) != 8 {
			return stat, errors.New(fmt.Sprintf("GetLastStat: Wrong stat length: %d != 8", len(row)))
		}
		for i := 1; i <= 7; i++ {
			if i == 4 {
				continue
			}
			statValues[i], err = row[i].(json.Number).Float64()
			if err != nil {
				return stat, errors.New("GetLastStat: Can't parse value: " + row[i].(string))
			}
		}

		stat.ContainerID = c.ID
		stat.CPUUsage = float64(statValues[1])
		stat.NetBandwithRX = float64(statValues[2])
		stat.NetBandwithTX = float64(statValues[3])
		stat.Running = row[4].(bool)
		stat.SizeMemory = float64(statValues[5])
		stat.SizeRootFs = float64(statValues[6])
		stat.SizeRw = float64(statValues[7])
	}

	return stat, err
}

/*
	Get stats by container id
*/
func GetStatsByContainerCID(containerCID string, o Options) ([]Stat, error) {
	var stats []Stat  // List of stats to return
	var query string  // InfluxDB query
	var oS, oB string // Query options
	var err error     // Error handling

	var sinceT, beforeT time.Time
	var betweenDuration time.Duration
	var groupByTime int

	// Check limitations
	if o.Limit > 90000 {
		return nil, errors.New(fmt.Sprintf("limit is to damn high! (%d)", o.Limit))
	}
	if o.Limit == -1 {
		o.Limit = 10
	}

	// Make InfluxDB query
	query = `	SELECT	mean(cpuusage) as cpuusage,
							mean(netbandwithrx) as netbandwithrx,
							mean(netbandwithtx) as netbandwithtx,
							mean(sizememory) as sizememory,
							mean(sizerootfs) as sizerootfs,
							mean(sizerw) as sizerw
					FROM cstats
					WHERE time < now()
					AND containerid = '` + containerCID + `'`

	// Add options
	if o.Since != "" || o.Before != "" {
		if o.Since != "" && o.Before != "" {
			oS = "'" + o.Since + "'"
			oB = "'" + o.Before + "'"
			sinceT, err = time.Parse(time.RFC3339, o.Since)
			beforeT, err = time.Parse(time.RFC3339, o.Before)
		} else if o.Since == "" || o.Before != "" {
			oS = "now() - 1d"
			oB = "'" + o.Before + "'"
			sinceT = time.Now().Add(time.Hour * (-24))
			beforeT, err = time.Parse(time.RFC3339, o.Before)
		} else if o.Since != "" || o.Before == "" {
			oS = "'" + o.Since + "'"
			oB = "now()"
			sinceT, err = time.Parse(time.RFC3339, o.Since)
			beforeT = time.Now()
		}
	} else {
		oS = "now() - 1d"
		oB = "now()"
		sinceT = time.Now().Add(time.Hour * (-24))
		beforeT = time.Now()
	}
	query += fmt.Sprintf(" AND time > %s AND time < %s ", oS, oB)

	// If limit is defined, calculate the interval
	if o.Limit != -1 {
		betweenDuration = beforeT.Sub(sinceT)
		groupByTime = int(float64(betweenDuration.Seconds()) / float64(o.Limit-1) * 1000)
		query += fmt.Sprintf(" GROUP BY time(%dms)", groupByTime)
	}

	// Send query
	l.Debug("GetStatsByContainerCID: ("+containerCID+") InfluxDB query:", query)
	res, err := queryDB(DB, query)
	if err != nil {
		return nil, err
	}

	// Check if not found
	if len(res) < 1 || len(res[0].Series) < 1 {
		return nil, errors.New("GetStatsByContainerCID: (" + containerCID + ") Not found")
	}

	// Get results
	for _, row := range res[0].Series[0].Values {
		var stat Stat
		var statValues [7]float64

		if len(row) != 7 {
			return nil, errors.New(fmt.Sprintf("GetStatsByContainerCID: ("+containerCID+") Wrong stat length: %d != 7", len(row)))
		}

		// Parse
		for i := 1; i <= 6; i++ {
			if row[i] == nil {
				statValues[i] = 0
			} else {
				statValues[i], err = row[i].(json.Number).Float64()
				if err != nil {
					return nil, errors.New("GetStatsByContainerCID: (" + containerCID + ") Can't parse value: " + fmt.Sprintf("%#v", row[i]))
				}
			}
		}

		// Set
		stat.Time, _ = time.Parse(time.RFC3339, row[0].(string))
		stat.ContainerID = containerCID
		stat.CPUUsage = float64(statValues[1])
		stat.NetBandwithRX = float64(statValues[2])
		stat.NetBandwithTX = float64(statValues[3])
		stat.SizeMemory = float64(statValues[4])
		stat.SizeRootFs = float64(statValues[5])
		stat.SizeRw = float64(statValues[6])

		stats = append(stats, stat)
	}

	return stats, nil
}

/*
	Get stats by probe name
*/
func GetStatsByContainerProbeID(probeName string, o Options) ([]Stat, error) {
	var stats []Stat  // List of stats to return
	var query string  // InfluxDB query
	var oS, oB string // Query options
	var err error     // Error handling

	var sinceT, beforeT time.Time
	var betweenDuration time.Duration
	var groupByTime int

	// Check limitations
	if o.Limit > 90000 {
		return nil, errors.New(fmt.Sprintf("limit is to damn high! (%d)", o.Limit))
	}
	if o.Limit == -1 {
		o.Limit = 10
	}

	// Make InfluxDB query
	query = `	SELECT	mean(cpuusage) as cpuusage,
							mean(netbandwithrx) as netbandwithrx,
							mean(netbandwithtx) as netbandwithtx,
							mean(sizememory) as sizememory,
							mean(sizerootfs) as sizerootfs,
							mean(sizerw) as sizerw
					FROM cstats
					WHERE time < now()
					AND probename = '` + probeName + `'`

	// Add options
	if o.Since != "" || o.Before != "" {
		if o.Since != "" && o.Before != "" {
			oS = "'" + o.Since + "'"
			oB = "'" + o.Before + "'"
			sinceT, err = time.Parse(time.RFC3339, o.Since)
			beforeT, err = time.Parse(time.RFC3339, o.Before)
		} else if o.Since == "" || o.Before != "" {
			oS = "now() - 1d"
			oB = "'" + o.Before + "'"
			sinceT = time.Now().Add(time.Hour * (-24))
			beforeT, err = time.Parse(time.RFC3339, o.Before)
		} else if o.Since != "" || o.Before == "" {
			oS = "'" + o.Since + "'"
			oB = "now()"
			sinceT, err = time.Parse(time.RFC3339, o.Since)
			beforeT = time.Now()
		}
	} else {
		oS = "now() - 1d"
		oB = "now()"
		sinceT = time.Now().Add(time.Hour * (-24))
		beforeT = time.Now()
	}
	query += fmt.Sprintf(" AND time > %s AND time < %s ", oS, oB)

	// Calculate limit interval
	betweenDuration = beforeT.Sub(sinceT)
	groupByTime = int(float64(betweenDuration.Seconds()) / float64(o.Limit-1) * 1000)
	query += fmt.Sprintf(" GROUP BY containerid,time(%dms)", groupByTime)

	// Send query
	l.Debug("GetStatsByContainerProbeID: ("+probeName+") InfluxDB query:", query)
	res, err := queryDB(DB, query)
	if err != nil {
		return nil, err
	}

	// Check if not found
	if len(res) < 1 || len(res[0].Series) < 1 {
		return nil, errors.New("GetStatsByContainerProbeID: (" + probeName + ") Not found")
	}

	// Get results
	for _, serie := range res[0].Series {
		var tmpStats []Stat
		var CID string
		// Get the tag (Container ID)
		for _, t := range serie.Tags {
			CID = t
			break
		}

		// Get all values
		for _, row := range serie.Values {
			var stat Stat
			var statValues [7]float64

			if len(row) != 7 {
				return nil, errors.New(fmt.Sprintf("GetStatsByContainerProbeID: ("+probeName+") Wrong stat length: %d != 7", len(row)))
			}

			// Parse
			for i := 1; i <= 6; i++ {
				if row[i] == nil {
					statValues[i] = 0
				} else {
					statValues[i], err = row[i].(json.Number).Float64()
					if err != nil {
						return nil, errors.New("GetStatsByContainerProbeID: (" + probeName + ") Can't parse value: " + fmt.Sprintf("%#v", row[i]))
					}
				}
			}

			// Set
			stat.Time, _ = time.Parse(time.RFC3339, row[0].(string))
			stat.ContainerID = CID
			stat.CPUUsage = float64(statValues[1])
			stat.NetBandwithRX = float64(statValues[2])
			stat.NetBandwithTX = float64(statValues[3])
			stat.SizeMemory = float64(statValues[4])
			stat.SizeRootFs = float64(statValues[5])
			stat.SizeRw = float64(statValues[6])

			tmpStats = append(tmpStats, stat)
		}

		// Append stats
		stats = append(stats, tmpStats...)
	}

	return stats, nil
}

/*
	Get stats populated by probe name
*/
func GetStatsPByContainerProbeID(probeName string, o Options) ([]StatPopulated, error) {
	var containers []dguard.Container // List of containers in the probe
	var statsP []StatPopulated        // List of stats populated to return
	var err error                     // Error handling

	// Get list of containers in the probe
	containers, err = GetContainersByProbe(probeName)
	if err != nil {
		return nil, err
	}

	// Get stats for each containers
	for _, container := range containers {
		tmpStats, err := GetStatsByContainerCID(container.ID, o)
		if err != nil {
			return nil, err
		}
		for _, tmpStat := range tmpStats {
			statP := StatPopulated{
				Container:     container,
				Time:          tmpStat.Time,
				SizeRootFs:    tmpStat.SizeRootFs,
				SizeRw:        tmpStat.SizeRw,
				SizeMemory:    tmpStat.SizeMemory,
				NetBandwithRX: tmpStat.NetBandwithRX,
				NetBandwithTX: tmpStat.NetBandwithTX,
				CPUUsage:      tmpStat.CPUUsage,
				Running:       tmpStat.Running,
			}

			statsP = append(statsP, statP)
		}
	}

	return statsP, nil
}
