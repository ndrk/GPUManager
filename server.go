package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"text/template"

	"golang.org/x/crypto/ssh"
)

func indexHandler(w http.ResponseWriter, r *http.Request) {
	type JSONResponse struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}

	type ServerInfo struct {
		Hostname, URL string
		InUse, Max    int
		PercentUsed   float64
	}

	rows, err := DB.Query("SELECT server.url, server_resource.inuse FROM server JOIN server_resource ON server.url=server_resource.server")
	if err != nil {
		json.NewEncoder(w).Encode(JSONResponse{Success: false, Message: err.Error()})
		return
	}

	var data []ServerInfo
	for rows.Next() {
		var name string
		var inuse bool
		if err := rows.Scan(&name, &inuse); err != nil {
			json.NewEncoder(w).Encode(JSONResponse{Success: false, Message: err.Error()})
			return
		}

		found := false
		for i := 0; i < len(data); i++ {
			if data[i].URL == name {
				found = true
				data[i].Max += 1
				if inuse {
					data[i].InUse += 1
				}
				break
			}
		}

		if !found {
			data = append(data, ServerInfo{URL: name, Hostname: strings.Split(name, ".")[0], InUse: 0, Max: 1, PercentUsed: 0})
		}
	}

	for i := 0; i < len(data); i++ {
		data[i].PercentUsed = (float64(data[i].InUse) / float64(data[i].Max)) * 100.0
	}

	t, _ := template.ParseFiles("index.html")
	t.Execute(w, data)
}

func serverAddHandker(w http.ResponseWriter, r *http.Request) {
	type ServerResponse struct {
		Hostname  string `json:"hostname"`
		URL       string `json:"url"`
		Resources int    `json:"resources"`
	}

	type JSONResponse struct {
		Success bool           `json:"success"`
		Message string         `json:"message"`
		Server  ServerResponse `json:"server"`
	}

	w.Header().Set("Content-Type", "application/json")

	if r.FormValue("server_name") == "" || r.FormValue("user_name") == "" || r.FormValue("password") == "" {
		json.NewEncoder(w).Encode(JSONResponse{Success: false, Message: "Missing Data"})
		return
	}

	// Test if we can connect
	config := &ssh.ClientConfig{
		User: r.FormValue("user_name"),
		Auth: []ssh.AuthMethod{
			ssh.Password(r.FormValue("password")),
		},
	}

	client, err := ssh.Dial("tcp", r.FormValue("server_name")+":22", config)
	if err != nil {
		json.NewEncoder(w).Encode(JSONResponse{Success: false, Message: fmt.Sprintf("Error connecting to %s. Please check the url and username/password.", r.FormValue("server_name"))})
		return
	}

	session, err := client.NewSession()
	if err != nil {
		json.NewEncoder(w).Encode(JSONResponse{Success: false, Message: "Unable to create session"})
		return
	}
	defer session.Close()

	result, err := session.CombinedOutput("nvidia-smi -L")
	if err != nil {
		json.NewEncoder(w).Encode(JSONResponse{Success: false, Message: "Unable to execute command"})
		return
	}

	server := Server{Hostname: strings.Split(r.FormValue("server_name"), ".")[0], URL: r.FormValue("server_name")}

	if _, err := DB.Exec("insert into server(url, username, password) values (?,?,?)", server.URL, r.FormValue("user_name"), r.FormValue("password")); err != nil {
		json.NewEncoder(w).Encode(JSONResponse{Success: false, Message: err.Error()})
		return
	}

	// Find GPUs
	scanner := bufio.NewScanner(bytes.NewReader(result))
	scanner.Split(bufio.ScanLines)

	re := regexp.MustCompile("GPU \\d+: (.+) \\(UUID: (.+)\\)")
	for scanner.Scan() {
		result := re.FindAllStringSubmatch(scanner.Text(), -1)
		if len(result) == 1 && len(result[0]) == 3 {
			session, err := client.NewSession()
			if err != nil {
				json.NewEncoder(w).Encode(JSONResponse{Success: false, Message: "Unable to create session"})
				return
			}

			res := Resource{Name: result[0][1], UUID: result[0][2], InUse: false, Connection: session}
			server.Resources = append(server.Resources, res)

			if _, err := DB.Exec("insert into server_resource(uuid, name, inuse, server) values (?,?,?,?)", res.UUID, res.Name, res.InUse, server.URL); err != nil {
				json.NewEncoder(w).Encode(JSONResponse{Success: false, Message: err.Error()})
				return
			}
		}
	}

	servers = append(servers, server)

	json.NewEncoder(w).Encode(JSONResponse{Success: true, Message: string(result), Server: ServerResponse{server.Hostname, server.URL, len(server.Resources)}})
}