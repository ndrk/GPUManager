package main

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

type Resource struct {
	DeviceID   int
	InUse      bool
	Name, UUID string
	Parent     *Server
}

func (r *Resource) Handle() {
	Log := log.New(os.Stdout, fmt.Sprintf("%s[%d] ", r.Parent.URL, r.DeviceID), log.Ltime|log.Lshortfile)

	for {
		time.Sleep(1 * time.Second)

		if !r.Parent.Enabled || r.InUse {
			continue
		}

		// Get Job
		jobInstance := <-JobQueue

		r.InUse = true

		time.Sleep(time.Duration(rand.Int31n(10)) * time.Second)
		Log.Println(jobInstance)

		if jobInstance.PID == -1 {
			// Send Model Data
			Log.Println("Uploading Model")
			sftp, err := sftp.NewClient(r.Parent.Client)
			if err != nil {
				Log.Fatal(err)
			}

			sftp.Mkdir(sftp.Join(r.Parent.WorkingDirectory, "model"))
			sftp.Mkdir(sftp.Join(r.Parent.WorkingDirectory, "model", strings.ToLower(jobInstance.Parent.Model.Name)))

			for _, file := range jobInstance.Parent.Model.Files {
				fIn, err := os.Open(fmt.Sprintf("data/%s/%s", jobInstance.Parent.Model.Name, file))
				if err != nil {
					Log.Fatalln(err)
				}
				defer fIn.Close()

				fOut, err := sftp.Create(sftp.Join(r.Parent.WorkingDirectory, "model", strings.ToLower(jobInstance.Parent.Model.Name), file))
				if err != nil {
					Log.Fatalln(err)
				}
				defer fIn.Close()

				io.Copy(fOut, fIn)
			}

			time.Sleep(1 * time.Second)
			// Send Template Data
			template, err := jobInstance.Parent.Template.Process(r.DeviceID, *jobInstance.Parent)
			if err != nil {
				Log.Fatalln(err)
			}

			sftp.Mkdir(sftp.Join(r.Parent.WorkingDirectory, "job"))
			sftp.Mkdir(sftp.Join(r.Parent.WorkingDirectory, "job", strings.ToLower(jobInstance.Parent.Name)))
			sftp.Mkdir(sftp.Join(r.Parent.WorkingDirectory, "job", strings.ToLower(jobInstance.Parent.Name), strings.ToLower(fmt.Sprintf("%d", jobInstance.NumberInSequence()))))

			fOut, err := sftp.Create(sftp.Join(r.Parent.WorkingDirectory, "job", strings.ToLower(jobInstance.Parent.Name), strings.ToLower(fmt.Sprintf("%d", jobInstance.NumberInSequence())), "sim.conf"))
			if err != nil {
				Log.Fatalln(err)
			}
			fOut.Write(template)
			fOut.Close()

			sftp.Close()

			time.Sleep(1 * time.Second)
			// Start job and retrieve PID
			Log.Println("Starting Job")
			session, err := r.Parent.Client.NewSession()
			if err != nil {
				Log.Fatalln(err)
			}

			command := "/bin/bash\n"
			command += "source ~/.bash_profile\n"
			if r.Parent.WorkingDirectory != "" {
				command += "cd " + r.Parent.WorkingDirectory + "\n"
			}
			command += strings.ToLower(fmt.Sprintf("cd job/%s/%d\n", jobInstance.Parent.Name, jobInstance.NumberInSequence()))
			command += "bash -c 'ProtoMol sim.conf &> Log.txt & echo $! > pidfile; wait $!; echo $? > exit-status' &> /dev/null &\n"
			command += "sleep 1\n"
			command += "cat pidfile"

			sPID, err := session.CombinedOutput(command)
			if err != nil {
				Log.Fatalln(string(sPID), err)
			}
			session.Close()

			// Parse PID
			pid, err := strconv.Atoi(strings.TrimSpace(string(sPID)))
			if err != nil {
				Log.Fatalln(err)
			}
			Log.Println("PID:", pid)

			jobInstance.PID = pid
			if _, err := DB.Exec("update job_instance set pid = ? where id = ?", jobInstance.PID, jobInstance.ID); err != nil {
				Log.Fatalln(err)
			}
		}

		time.Sleep(1 * time.Second)
		// Wait for completion
		Log.Println("Waiting for completion")
		session, err := r.Parent.Client.NewSession()
		if err != nil {
			Log.Fatalln(err)
		}

		command := ""
		if r.Parent.WorkingDirectory != "" {
			command += "cd " + r.Parent.WorkingDirectory + "\n"
		}
		command += strings.ToLower(fmt.Sprintf("cd job/%s/%d\n", jobInstance.Parent.Name, jobInstance.NumberInSequence()))
		command += fmt.Sprintf("bash -c 'while [[ ( -d /proc/%d ) && ( -z `grep zombie /proc/%d/status` ) ]]; do sleep 1; done; sleep 1; cat exit-status'", jobInstance.PID, jobInstance.PID)

		output, err := session.CombinedOutput(command)
		if err != nil {
			Log.Fatalln(string(output), err)
		}

		exitcode, err := strconv.Atoi(strings.TrimSpace(string(output)))
		if err != nil {
			Log.Fatalln(err)
		}
		Log.Println("Exit Code:", exitcode)

		jobInstance.Completed = true
		if _, err := DB.Exec("update job_instance set completed = ? where id = ?", jobInstance.Completed, jobInstance.ID); err != nil {
			Log.Fatalln(err)
		}
		r.InUse = false
	}
}
