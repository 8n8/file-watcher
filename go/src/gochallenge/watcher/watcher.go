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
)

type fileChangeT bool

const (
	createFile = true
	deleteFile = false
)

type changeSet struct {
	guid string
	fileName string
	fileChange fileChangeT
}

type stateT struct {
	folder map[string]bool
	newestChange changeSet
	lastChangeGuid string
	keepGoing bool
	fatalError error
	nonFatalError error
	ignorantMaster bool
}

func initState() stateT {
	return stateT {
		folder: map[string]bool{},
		newestChange: changeSet{
			guid: "no guid because first time",
			fileName: "no file name because first time",
			fileChange: createFile,
		},
		lastChangeGuid: "no guid because first time",
		keepGoing: true,
		fatalError: nil,
		nonFatalError: nil,
		ignorantMaster: true,
	}
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
			Guid: newestChange.guid,
			AllFiles: getKeys(folder),
			Directory: dirToWatch,
			NewList: true,
		}
	} else {
		result = msgToMaster{
			Guid:         newestChange.guid,
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
	jsonMsg, encErr := createPostMsg(
		state.ignorantMaster,
		state.newestChange,
		state.folder,
		state.lastChangeGuid,
		dirToWatch)
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
	fmt.Println("a")

	if output.msgToPrint != "" {
		log.Print(output.msgToPrint)
	}

	result := initIoResult()

	if output.checkForFileChanges {

		guidBytes, _ := uuid.NewV4()
		result.newGuid = guidBytes.String()

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

	response, postErr := http.Post(
		masterUrl,
		"application/json",
		bytes.NewBuffer(output.jsonToSend))
	defer response.Body.Close()
	result.requestErr = postErr
	if postErr != nil { return result }
	body, bodyErr := ioutil.ReadAll(response.Body)
	fmt.Println("========Out========")
	fmt.Println(string(output.jsonToSend))
	fmt.Println(">>>>>>>>Back>>>>>>>>")
	fmt.Println(string(body))
	fmt.Println("<<<<<<<<End<<<<<<<<")
	result.responseBody = body
	result.readBodyErr = bodyErr

	return result
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

func newChangeSet(fileEvent fsnotify.Event, newGuid string) changeSet {
	return changeSet {
		guid: newGuid,
		fileName: filepath.Base(fileEvent.Name),
		fileChange: opMap()[fileEvent.Op],
	}
}

func update(state stateT, ioResult ioResultT) stateT {
	newChanges := changeSet{}
	newFolder := map[string]bool{}
	if ioResult.newEvents {
		newChanges = newChangeSet(ioResult.fileEvent, ioResult.newGuid)
		newFolder = updateFolder(state.folder, newChanges)
	} else {
		newChanges = state.newestChange
		newFolder = state.folder
	}

	fmt.Println("new changes start:")
	fmt.Println(newChanges)
	fmt.Println("new changes end")
	fmt.Println(state)
	fmt.Println(state.newestChange.guid)
	stateAfter := stateT{
		fatalError: fatalErrors(ioResult.fileError, ioResult.eventChOk, ioResult.errChOk),
		keepGoing: state.fatalError == nil,
		folder: newFolder,
		lastChangeGuid: state.newestChange.guid,
		newestChange: newChanges,
		ignorantMaster: string(ioResult.responseBody) == "badGuid",
		nonFatalError: combineErrors([]error{ioResult.requestErr, ioResult.readBodyErr}),
	}
	fmt.Println("stateAfter")
	fmt.Println(stateAfter)
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
	dirToWatch := "/home/true/toWatch"
	masterUrl := "http://localhost:3000"

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

	state := initState()
	for state.keepGoing {
		state = update(state, io(watCh, stateToOutput(state, dirToWatch), masterUrl))
	}
}