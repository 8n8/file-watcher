// Provides the two main functions for the main loop of the watcher
// server.  One to update the state, and the other to calculate
// the messages to send out, given the state.
package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"path/filepath"
	"strings"
)

// The state of the main loop of the watcher server.
type StateT struct {
	Folder         map[string]bool
	NewChanges     bool
	NewestChange   changeSet
	LatestGuid     string
	PreviousGuid   string
	KeepGoing      bool
	FatalError     error
	NonFatalError  error
	IgnorantMaster bool
}

func InitState(fileList map[string]bool, newGuid string) StateT {
	return StateT{
		Folder:     fileList,
		NewChanges: true,
		NewestChange: changeSet{
			fileName:   "",
			fileChange: createFile,
		},
		LatestGuid:     newGuid,
		PreviousGuid:   "",
		KeepGoing:      true,
		FatalError:     nil,
		NonFatalError:  nil,
		IgnorantMaster: true,
	}
}

type changeSet struct {
	fileName   string
	fileChange fileChangeT
}

func InitIoResult() IoResultT {
	return IoResultT{
		FileEvent:    fsnotify.Event{},
		NewEvents:    false,
		FileError:    nil,
		ErrChOk:      true,
		EventChOk:    true,
		RequestErr:   nil,
		ResponseBody: nil,
		ReadBodyErr:  nil,
		NewGuid:      "",
	}
}

type IoResultT struct {
	FileEvent    fsnotify.Event
	FileError    error
	ErrChOk      bool
	EventChOk    bool
	RequestErr   error
	ResponseBody []byte
	ReadBodyErr  error
	NewGuid      string
	NewEvents    bool
}

type fileChangeT bool

const (
	createFile = true
	deleteFile = false
)

func opMap() map[fsnotify.Op]fileChangeT {
	opMap := map[fsnotify.Op]fileChangeT{}
	opMap[fsnotify.Create] = createFile
	opMap[fsnotify.Remove] = deleteFile
	return opMap
}

func updateFolder(folder map[string]bool, changes changeSet) map[string]bool {
	result := folder
	if changes.fileChange == createFile {
		result[changes.fileName] = true
	} else {
		delete(result, changes.fileName)
	}
	return result
}

func newChangeSet(fileEvent fsnotify.Event) changeSet {
	return changeSet{
		fileName:   filepath.Base(fileEvent.Name),
		fileChange: opMap()[fileEvent.Op],
	}
}

// The main update function used by the main loop of the server to update
// the state, given the previous state and new data from IO.
func Update(state StateT, ioResult IoResultT) StateT {
	newestChanges := changeSet{}
	newFolder := map[string]bool{}
	var latestGuid string
	var previousGuid string
	if ioResult.NewEvents {
		newestChanges = newChangeSet(ioResult.FileEvent)
		newFolder = updateFolder(state.Folder, newestChanges)
		latestGuid = ioResult.NewGuid
		previousGuid = state.LatestGuid
	} else {
		newestChanges = state.NewestChange
		newFolder = state.Folder
		latestGuid = state.LatestGuid
		previousGuid = state.PreviousGuid
	}

	stateAfter := StateT{
		FatalError:     fatalErrors(ioResult.FileError, ioResult.EventChOk, ioResult.ErrChOk),
		KeepGoing:      state.FatalError == nil,
		Folder:         newFolder,
		PreviousGuid:   previousGuid,
		LatestGuid:     latestGuid,
		NewChanges:     ioResult.NewEvents,
		NewestChange:   newestChanges,
		IgnorantMaster: string(ioResult.ResponseBody) == "badGuid",
		NonFatalError:  combineErrors([]error{ioResult.RequestErr, ioResult.ReadBodyErr}),
	}
	return stateAfter
}

// It takes in the parts of the main loop state which are considered to be fatal
// errors, and combines them into a single error.
func fatalErrors(fileError error, eventChOk bool, errChOk bool) error {
	errSlice := []error{fileError}
	if !eventChOk {
		errSlice = append(errSlice, errors.New("event channel broken"))
	}
	if !errChOk {
		errSlice = append(errSlice, errors.New("error channel broken"))
	}
	return combineErrors(errSlice)
}

func combineErrors(errors []error) error {
	var noNils []error
	for _, err := range errors {
		if err != nil {
			noNils = append(noNils, err)
		}
	}
	if len(noNils) == 0 {
		return nil
	}
	return fmt.Errorf(strings.Join(errorsToStrings(noNils), "\n"))
}

func errorsToStrings(errors []error) []string {
	var strs []string
	for _, err := range errors {
		strs = append(strs, err.Error())
	}
	return strs
}

// It produces the output messages, given the current state.
func StateToOutput(state StateT, dirToWatch string) OutputT {
	var jsonMsg []byte
	var encErr error
	if state.NewChanges || state.IgnorantMaster {
		jsonMsg, encErr = createPostMsg(
			state.IgnorantMaster,
			state.LatestGuid,
			state.NewestChange,
			state.Folder,
			state.PreviousGuid,
			dirToWatch)
	} else {
		jsonMsg = []byte{}
		encErr = nil
	}
	errs := combineErrors([]error{state.FatalError, state.NonFatalError, encErr})
	var msgToPrint string
	if errs == nil {
		msgToPrint = ""
	} else {
		msgToPrint = errs.Error()
	}
	return OutputT{
		JsonToSend: jsonMsg,
		MsgToPrint: msgToPrint,
	}
}

type OutputT struct {
	JsonToSend []byte
	MsgToPrint string
}

// Makes the message to put into the POST body to send to the master server.
func createPostMsg(
	ignorantMaster bool,
	latestGuid string,
	newestChange changeSet,
	folder map[string]bool,
	previousGuid string,
	dirToWatch string) ([]byte, error) {
	var result msgToMaster
	if ignorantMaster {
		result = msgToMaster{
			FileName:     newestChange.fileName,
			PreviousGuid: previousGuid,
			ChangeType:   changeTypeMap()[newestChange.fileChange],
			Guid:         latestGuid,
			AllFiles:     getKeys(folder),
			Directory:    dirToWatch,
			NewList:      true,
		}
	} else {
		result = msgToMaster{
			Guid:         latestGuid,
			PreviousGuid: previousGuid,
			FileName:     newestChange.fileName,
			ChangeType:   changeTypeMap()[newestChange.fileChange],
			AllFiles:     []string{},
			Directory:    dirToWatch,
			NewList:      false,
		}
	}
	jsn, err := json.Marshal(result)
	return jsn, err
}

func changeTypeMap() map[fileChangeT]string {
	m := map[fileChangeT]string{}
	m[createFile] = "create"
	m[deleteFile] = "delete"
	return m
}

type msgToMaster struct {
	Guid         string
	PreviousGuid string
	FileName     string
	ChangeType   string
	AllFiles     []string
	Directory    string
	NewList      bool
}

func getKeys(m map[string]bool) []string {
	result := make([]string, len(m))
	i := 0
	for k := range m {
		result[i] = k
		i++
	}
	return result
}
