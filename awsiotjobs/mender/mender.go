package mender

import (
	"encoding/json"
	"fmt"
	"log"
	"mendercmd"
	"os/exec"
	"time"

	"../../awsiotjobs"
)

var timeout = 10 * time.Minute

type nextJobPayload struct {
	clientToken string
}

type jobDocument struct {
	operation  string
	parameters string
}

type menderInstall struct {
	url string
}

// Job represents the job document received via AWS IoT jobs
type Job struct {
	Operation   string `json:"operation"`
	URL         string `json:"url"`
	menderState State
	execution   awsiotjobs.JobExecutioner
}

// State reports the state of the job
type State struct {
	Step string `json:"step"`
}

func (mj *Job) progress(step string) {
	mj.menderState.Step = step // should wrap with a mutex
	err := mj.execution.InProgress(awsiotjobs.StatusDetails{"step": step})
	if err != nil {
		log.Printf("Failed to execute InProgress on the Job, got error: %s", err.Error())
	}
}

func (mj *Job) success(step string) {
	mj.menderState.Step = step // should wrap with a mutex
	err := mj.execution.Success(awsiotjobs.StatusDetails{"step": step})
	if err != nil {
		log.Printf("Failed to execute Success on the Job, got error: %s", err.Error())
	}
}

func (mj *Job) fail(err awsiotjobs.JobError) {
	e := mj.execution.Fail(err)
	if e != nil {
		log.Printf("Failed to execute Fail on the Job, got error: %s", err.Error())
	}
}

func (mj *Job) reject(err awsiotjobs.JobError) {
	e := mj.execution.Reject(err)
	if e != nil {
		log.Printf("Failed to execute Reject on the Job, got error: %s", err.Error())
	}
}

// This function implements the logic for the execution of the Mender job
func (mj *Job) exec(cmd mendercmd.Commander, timeout time.Duration) error {
	switch mj.Operation {
	case "mender_install":
		// check if we are back after rebooting
		switch mj.menderState.Step {
		case "rebooting":
			mj.reportProgress("rebooted")
			// This is a naive implementation. Before committing one would probably check the system is working fine
			// and then issue the Commit, otherwise Rollback.
			// For example, in case Greengrass was installed, one could check that Greengrass service is up
			// and running.
			// On the other hand, to come to this stage, we know that we have network, time and date and we can connect
			// to AWS.
			err := cmd.Commit() // commit
			if err != nil {
				jobErr := awsiotjobs.JobError{ErrCode: "ERR_MENDER_COMMIT", ErrMessage: "error committing"}
				mj.fail(jobErr)
				return jobErr
			}
			mj.success("committed")
		default:
			// If the step is "installing" it could be for different cases
			// 1- the system rebooted/lost connection and the installation was not completed.
			// 2- Installation was completed, system rebooted, but the state update was not performed
			// In case 1 we should restart the installation process
			// In case 2 we should either make sure this does not happen - ie make the reboot conditional to the
			// correct persistance of the "rebooting" state; or rely on some other mechanism to detect that the
			// firmware has been successfully updated and the system has rebooted and is working correctly

			ch := make(chan string)
			done := make(chan error)
			mj.progress("installing")
			go cmd.Install(mj.URL, done, ch)
			for {
				select {
				case progress := <-ch:
					log.Printf("%s", progress)
					mj.reportProgress(progress) // report progress via MQTT
				case err := <-done:
					if err != nil {
						jobErr := awsiotjobs.JobError{ErrCode: "ERR_MENDER_INSTALL_FAILED", ErrMessage: err.Error()}
						mj.fail(jobErr)
						return jobErr
					}
					// This should be changed - setting the rebooting state might fail
					// and when the system startsup will find a wrong state and will start installing the software again
					// Must find a way to make this deterministic - maybe relying on mender local state?
					mj.progress("rebooting")
					go func() {
						cmd := exec.Command("shutdown", "-r", "now")
						cmd.Start()
						err := cmd.Wait()
						if err != nil {
							fmt.Println("Could not reboot the system")
							mj.fail(awsiotjobs.JobError{ErrCode: "ERROR_UNABLE_TO_REBOOT", ErrMessage: err.Error()})
							return
						}
						fmt.Println("rebooting...")
						mj.execution.Terminate() //Should be called by the agent code and not the library - based on signalling from the OS when shutting down
					}()
					return nil
				case <-time.After(timeout): // timeout value can be in doc
					fmt.Printf("install timeout")
					jobErr := awsiotjobs.JobError{ErrCode: "ERR_MENDER_INSTALL_TIMEOUT", ErrMessage: "mender timed out"}
					mj.fail(jobErr)
					return jobErr
				}
			}
		}

	case "mender_rollback":
		err := cmd.Rollback()
		if err != nil {
			mj.fail(awsiotjobs.JobError{ErrCode: "ERR_MENDER_ROLLBACK_FAIL", ErrMessage: "unable to run rollback"})
			return err
		}
		mj.success("rolled_back")
	}
	return nil
}

func (mj *Job) reportProgress(p string) {
	payload := map[string]interface{}{
		"progress": p,
		"ts":       time.Now().Unix(),
	}
	topic := fmt.Sprintf("mender/%s/job/%s/progress", mj.execution.GetThingName(), mj.execution.GetJobID())
	jsonPayload, _ := json.Marshal(payload)
	mj.execution.Publish(topic, 0, jsonPayload)
}

func parseJobDocument(jobExecution awsiotjobs.JobExecutioner) (Job, error) {
	jobDocument, _ := json.Marshal(jobExecution.GetJobDocument())
	job := Job{execution: jobExecution}
	json.Unmarshal(jobDocument, &job)
	switch job.Operation {
	case "mender_install":
		if len(job.URL) == 0 {
			return job, awsiotjobs.JobError{ErrCode: "ERR_MENDER_MISSING_URL", ErrMessage: "missing url parameter"}
		}
	case "mender_rollback":
	default:
		return job, awsiotjobs.JobError{ErrCode: "ERR_JOB_INVALID_OPERATION", ErrMessage: "unrecognized or missing operation"}
	}
	var menderState State
	statusDetails, _ := json.Marshal(jobExecution.GetStatusDetails())
	json.Unmarshal(statusDetails, &menderState)
	job.menderState = menderState
	return job, nil
}

// Process is the JobExecution handler
func Process(jobExecution awsiotjobs.JobExecutioner) {
	job, err := parseJobDocument(jobExecution)
	if err != nil {
		jobError, ok := err.(awsiotjobs.JobError)
		if ok {
			switch jobError.ErrCode {
			case "ERR_MENDER_MISSING_URL":
			case "ERR_JOB_INVALID_OPERATION":
				fmt.Printf("Invalid job document - Rejecting\n")
				job.reject(err.(awsiotjobs.JobError))
			default:
				fmt.Printf("Unknown - Ignoring")
			}
		} else {
			fmt.Printf("Unknown error %s - Ignoring\n", err.Error())
		}
	} else {
		go func() {
			job.exec(&mendercmd.MenderCommand{}, timeout)
		}()
	}
}
