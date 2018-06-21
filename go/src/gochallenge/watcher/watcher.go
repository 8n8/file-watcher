package main

import (
	"github.com/fsnotify/fsnotify"
	"github.com/nu7hatch/gouuid"
	"log"
	"errors"
	"fmt"
	"strings"
	"encoding/json"
	"path/filepath"
	"net/http"
	"bytes"
	"io/ioutil"
	"os"
)

type fileChangeT bool

const (
	createFile = true
	deleteFile = false
)

type changeSet struct {
	fileName string
	fileChange fileChangeT
}

type stateT struct {
	folder map[string]bool
	newChanges bool
	newestChange changeSet
	latestGuid string
	previousGuid string
	keepGoing bool
	fatalError error
	nonFatalError error
	ignorantMaster bool
}

func getFileNames(fileInfos []os.FileInfo) []string {
	result := make([]string, len(fileInfos))
	for i, inf := range fileInfos {
		result[i] = inf.Name()
	}
	return result
}

func initState(fileList map[string]bool) stateT {
	guid := newGuid()
	return stateT {
		folder: fileList,
		newChanges: true,
		newestChange: changeSet{
			fileName: "",
			fileChange: createFile,
		},
		latestGuid: guid,
		previousGuid: "",
		keepGoing: true,
		fatalError: nil,
		nonFatalError: nil,
		ignorantMaster: true,
	}
}

func listToMap(list []string) map[string]bool {
	m := map[string]bool{}
	for _, l := range list {
		m[l] = true
	}
	return m
}

type outputT struct {
	jsonToSend []byte
	msgToPrint string
	checkForFileChanges bool
}

type msgToMaster struct {
	Guid string
	PreviousGuid string
	FileName string
	ChangeType string
	AllFiles []string
	Directory string
	NewList bool
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
			FileName: newestChange.fileName,
			PreviousGuid: previousGuid,
			ChangeType: changeTypeMap()[newestChange.fileChange],
			Guid: latestGuid,
			AllFiles: getKeys(folder),
			Directory: dirToWatch,
			NewList: true,
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

func stateToOutput(state stateT, dirToWatch string) outputT {
	var jsonMsg []byte
	var encErr error
	if state.newChanges || state.ignorantMaster {
		jsonMsg, encErr = createPostMsg(
			state.ignorantMaster,
			state.latestGuid,
			state.newestChange,
			state.folder,
			state.previousGuid,
			dirToWatch)
	} else {
		jsonMsg = []byte{}
		encErr = nil
	}
	errs := combineErrors([]error{state.fatalError, state.nonFatalError, encErr})
	var msgToPrint string
	if errs == nil {
		msgToPrint = ""
	} else {
		msgToPrint = errs.Error()
	}
	return outputT{
		jsonToSend: jsonMsg,
		msgToPrint: msgToPrint,
		checkForFileChanges: !state.ignorantMaster,
	}
}

type ioResultT struct {
	fileEvent fsnotify.Event
	fileError error
	errChOk bool
	eventChOk bool
	requestErr error
	responseBody []byte
	readBodyErr error
	newGuid string
	newEvents bool
}

func initIoResult() ioResultT {
	return ioResultT{
		fileEvent: fsnotify.Event{},
		newEvents: false,
		fileError: nil,
		errChOk: true,
		eventChOk: true,
		requestErr: nil,
		responseBody: nil,
		readBodyErr: nil,
		newGuid: "",
	}
}

func io(watCh watcherChannelsT, output outputT, masterUrl string) ioResultT {

	if output.msgToPrint != "" {
		log.Print(output.msgToPrint)
	}

	result := initIoResult()

	if len(output.jsonToSend) > 0 {
		response, postErr := http.Post(
			masterUrl,
			"application/json",
			bytes.NewBuffer(output.jsonToSend))
		result.requestErr = postErr
		if postErr != nil { return result }
		defer response.Body.Close()
		body, bodyErr := ioutil.ReadAll(response.Body)
		result.responseBody = body
		result.readBodyErr = bodyErr
		return result
	}

	if output.checkForFileChanges {

		result.newGuid = newGuid()

		event, eventsChOk := <-watCh.events
		result.eventChOk = eventsChOk
		result.fileEvent = event

		select {
		case fileError, errChOk := <-watCh.errs:
			result.errChOk = errChOk
			result.fileError = fileError
		default:
		}
		result.newEvents = true
	}

	return result
}

func newGuid() string {
	guidBytes, _ := uuid.NewV4()
	return guidBytes.String()
}

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
	if len(noNils) == 0 { return nil }
	return fmt.Errorf(strings.Join(errorsToStrings(noNils), "\n"))
}

func errorsToStrings(errors []error) []string {
	var strs []string
	for _, err := range errors {
		strs = append(strs, err.Error())
	}
	return strs
}

func opMap() map[fsnotify.Op]fileChangeT {
	opMap := map[fsnotify.Op]fileChangeT{}
	opMap[fsnotify.Create] = createFile
	opMap[fsnotify.Remove] = deleteFile
	return opMap
}

func newChangeSet(fileEvent fsnotify.Event) changeSet {
	return changeSet {
		fileName: filepath.Base(fileEvent.Name),
		fileChange: opMap()[fileEvent.Op],
	}
}

func update(state stateT, ioResult ioResultT) stateT {
	newestChanges := changeSet{}
	newFolder := map[string]bool{}
	var latestGuid string
	var previousGuid string
	if ioResult.newEvents {
		newestChanges = newChangeSet(ioResult.fileEvent)
		newFolder = updateFolder(state.folder, newestChanges)
		latestGuid = ioResult.newGuid
		previousGuid = state.latestGuid
	} else {
		newestChanges = state.newestChange
		newFolder = state.folder
		latestGuid = state.latestGuid
		previousGuid = state.previousGuid
	}

	stateAfter := stateT{
		fatalError: fatalErrors(ioResult.fileError, ioResult.eventChOk, ioResult.errChOk),
		keepGoing: state.fatalError == nil,
		folder: newFolder,
		previousGuid: previousGuid,
		latestGuid: latestGuid,
		newChanges: ioResult.newEvents,
		newestChange: newestChanges,
		ignorantMaster: string(ioResult.responseBody) == "badGuid",
		nonFatalError: combineErrors([]error{ioResult.requestErr, ioResult.readBodyErr}),
	}
	return stateAfter
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

type watcherChannelsT struct {
	events chan fsnotify.Event
	errs chan error
	ask chan bool
}

const maxChanBuf = 10000

func main() {
	dirToWatch := os.Args[1]
	masterUrl := "http://localhost:3000"
	fileList, err := ioutil.ReadDir(dirToWatch)

	if err != nil {
		fmt.Println(err)
		return
	}

	watCh := watcherChannelsT{
		events: make(chan fsnotify.Event, maxChanBuf),
		errs: make(chan error, maxChanBuf),
		ask: make(chan bool),
	}

	go func() {
		watcher, err := fsnotify.NewWatcher()
		watcher.Add(dirToWatch)
		if err != nil {
			watCh.errs <- err
			return
		}
		for {
			select {
			case event := <-watcher.Events:
				if event.Op != fsnotify.Write && event.Op != fsnotify.Chmod {
					watCh.events <- event
				}
			case err := <-watcher.Errors:
				watCh.errs <- err
			}
		}
	}()

	state := initState(listToMap(getFileNames(fileList)))
	for state.keepGoing {
		state = update(state, io(watCh, stateToOutput(state, dirToWatch), masterUrl))
	}
}
