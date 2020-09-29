package mender

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aws-samples/aws-iot-jobs-full-system-update/goagent/awsiotjobs"
	"github.com/stretchr/testify/mock"
)

const testTimeout = 500 * time.Millisecond

type JobExecutionMock struct {
	mock.Mock
	jobExecution awsiotjobs.JobExecution
}

func (j *JobExecutionMock) GetStatusDetails() awsiotjobs.StatusDetails {
	j.On("GetStatusDetails").Return(j.jobExecution.StatusDetails)
	j.Called()
	return j.jobExecution.StatusDetails
}

func (j *JobExecutionMock) GetJobDocument() awsiotjobs.JobDocument {
	j.On("GetJobDocument").Return(j.jobExecution.JobDocument)
	return j.jobExecution.JobDocument
}

func (j *JobExecutionMock) GetJobID() string {
	j.On("GetJobID").Return("AA")
	j.Called()
	return "AA"
}

func (j *JobExecutionMock) GetThingName() string {
	j.On("GetThingName").Return("thingName")
	j.Called()
	return "thingName"
}

func (j *JobExecutionMock) Publish(t string, q byte, p interface{}) {
	j.On("Publish").Return()
	j.Called()
}

func (j *JobExecutionMock) Success(s awsiotjobs.StatusDetails) {
	j.On("Success").Return()
	j.Called()
}

func (j *JobExecutionMock) InProgress(s awsiotjobs.StatusDetails) {
	j.On("InProgress").Return()
	j.Called()
}

func (j *JobExecutionMock) Fail(e awsiotjobs.JobError) {
	j.On("Fail").Return()
	j.Called()
}

func (j *JobExecutionMock) Reject(e awsiotjobs.JobError) {
	j.On("Reject").Return()
	j.Called()
}

func (j *JobExecutionMock) Terminate() {
	j.On("Terminate").Return()
	j.Called()
}

func TestParseJobMessageInstall(t *testing.T) {
	doc := awsiotjobs.JobExecution{
		JobDocument: map[string]interface{}{
			"operation": "mender_install",
			"url":       "http://test",
		},
		Status:        "QUEUED",
		StatusDetails: map[string]interface{}{},
	}
	amock := JobExecutionMock{jobExecution: doc}
	job, _ := parseJobDocument(&amock)
	wanted := Job{
		"mender_install",
		"http://test",
		State{},
		&amock,
	}

	if !reflect.DeepEqual(job, wanted) {
		t.Errorf("\nwanted: %v,\ngot     %v", wanted, job)
	}

}

func TestParseJobMessageRollback(t *testing.T) {
	doc := awsiotjobs.JobExecution{
		JobID:     "job",
		ThingName: "thing",
		JobDocument: map[string]interface{}{
			"operation": "mender_rollback",
		},
		Status:          "QUEUED",
		StatusDetails:   map[string]interface{}{},
		QueuedAt:        1244423223,
		StartedAt:       1244423223,
		LastUpdatedAt:   1244423223,
		VersionNumber:   1,
		ExecutionNumber: 1000,
	}
	amock := JobExecutionMock{jobExecution: doc}
	job, _ := parseJobDocument(&amock)
	wanted := Job{
		"mender_rollback",
		"",
		State{},
		&amock,
	}

	if !reflect.DeepEqual(job, wanted) {
		t.Errorf("\nwanted: %v,\ngot     %v", wanted, job)
	}

}

func TestParseJobMessageInstallMissingUrl(t *testing.T) {
	doc := awsiotjobs.JobExecution{
		JobID:     "job",
		ThingName: "thing",
		JobDocument: map[string]interface{}{
			"operation": "mender_install",
		},
		Status:          "QUEUED",
		StatusDetails:   map[string]interface{}{},
		QueuedAt:        1244423223,
		StartedAt:       1244423223,
		LastUpdatedAt:   1244423223,
		VersionNumber:   1,
		ExecutionNumber: 1000,
	}

	amock := JobExecutionMock{jobExecution: doc}
	_, err := parseJobDocument(&amock)
	wanted := awsiotjobs.JobError{ErrCode: "ERR_MENDER_MISSING_URL", ErrMessage: "missing url parameter"}
	if err != wanted {
		t.Errorf("wanted %v got %v", wanted, err)
	}
}

func TestProcessMissingOperationFail(t *testing.T) {
	doc := awsiotjobs.JobExecution{
		JobDocument: map[string]interface{}{
			"operation": "mender_insll",
		},
		Status:        "QUEUED",
		StatusDetails: map[string]interface{}{},
	}
	amock := JobExecutionMock{jobExecution: doc}
	Process(&amock)
	amock.AssertCalled(t, "Reject")
}

func TestProcessMissingUrlFail(t *testing.T) {
	doc := awsiotjobs.JobExecution{
		JobDocument: map[string]interface{}{
			"operation": "mender_installl",
		},
		Status:        "QUEUED",
		StatusDetails: map[string]interface{}{},
	}
	amock := JobExecutionMock{jobExecution: doc}
	Process(&amock)
	amock.AssertCalled(t, "Reject")
}

type CommandFail struct {
	mock.Mock
}

func (c *CommandFail) Install(url string, done chan error, progress chan string) error {
	done <- errors.New("install error")
	return errors.New("install error")
}

func (c *CommandFail) Commit() error {
	//ret := c.Called()
	return errors.New("commit error")
}

func (c *CommandFail) Rollback() error {
	//ret := c.Called()
	return errors.New("rollback error")
}

func TestExecInstallFail(t *testing.T) {
	doc := awsiotjobs.JobExecution{
		JobDocument: map[string]interface{}{
			"operation": "mender_install",
		},
		Status:        "QUEUED",
		StatusDetails: map[string]interface{}{},
		VersionNumber: 1,
	}

	amock := JobExecutionMock{jobExecution: doc}

	job, _ := parseJobDocument(&amock)
	cmd := &CommandFail{}
	err := job.exec(cmd, testTimeout)
	time.Sleep(1 * time.Second)
	jobError, ok := err.(awsiotjobs.JobError)
	if !ok {
		t.Errorf("Expected JobError got %v", err)
	}
	wanted := "ERR_MENDER_INSTALL_FAILED"
	if jobError.ErrCode != wanted {
		t.Errorf("Expected \"%s\", got \"%s\"", wanted, jobError.ErrCode)
	}
	amock.AssertCalled(t, "Fail")
}

func TestExecCommitFail(t *testing.T) {
	doc := awsiotjobs.JobExecution{
		JobDocument: awsiotjobs.JobDocument{
			"operation": "mender_install",
			"url":       "http://test",
		},
		Status:        "QUEUED",
		StatusDetails: awsiotjobs.StatusDetails{"step": "rebooting"},
		VersionNumber: 1,
	}

	amock := JobExecutionMock{jobExecution: doc}

	job, _ := parseJobDocument(&amock)
	cmd := &CommandFail{}
	err := job.exec(cmd, testTimeout)
	time.Sleep(1 * time.Second)
	jobError, ok := err.(awsiotjobs.JobError)
	if !ok {
		t.Errorf("Expected JobError got %v", err)
	}
	wanted := "ERR_MENDER_COMMIT"
	if jobError.ErrCode != wanted {
		t.Errorf("Expected \"%s\", got \"%s\"", wanted, jobError.ErrCode)
	}
	amock.AssertCalled(t, "Fail")
}

type CommandTimeout struct {
	mock.Mock
}

func (c *CommandTimeout) Install(url string, done chan error, progress chan string) error {
	time.Sleep(testTimeout * 2)
	return nil
}

func (c *CommandTimeout) Commit() error {
	//ret := c.Called()
	return errors.New("commit error")
}

func (c *CommandTimeout) Rollback() error {
	//ret := c.Called()
	return errors.New("rollback error")
}
func TestExecTimeoutFail(t *testing.T) {
	doc := awsiotjobs.JobExecution{
		JobDocument: awsiotjobs.JobDocument{
			"operation": "mender_install",
			"url":       "http://test",
		},
		Status:        "QUEUED",
		StatusDetails: awsiotjobs.StatusDetails{},
		VersionNumber: 1,
	}

	amock := JobExecutionMock{jobExecution: doc}

	job, _ := parseJobDocument(&amock)
	cmd := &CommandTimeout{}
	err := job.exec(cmd, testTimeout)
	time.Sleep(1 * time.Second)
	jobError, ok := err.(awsiotjobs.JobError)
	if !ok {
		t.Errorf("Expected JobError got %v", err)
	}
	wanted := "ERR_MENDER_INSTALL_TIMEOUT"
	if jobError.ErrCode != wanted {
		t.Errorf("Expected \"%s\", got \"%s\"", wanted, jobError.ErrCode)
	}
	amock.AssertCalled(t, "Fail")
}
