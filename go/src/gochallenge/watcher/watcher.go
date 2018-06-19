package main

import (
	"github.com/fsnotify/fsnotify"
	"github.com/nu7hatch/gouuid"
	"net/http"
	"bytes"
	"io/ioutil"
	"log"
	"errors"
	"fmt"
	"strings"
	"encoding/json"
)

type changeSet struct {
	guid string
	deletions map[string]bool
	creations map[string]bool
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
		newestChange: changeSet{},
		lastChangeGuid: "",
		keepGoing: true,
		fatalError: nil,
		nonFatalError: nil,
		ignorantMaster: false,
	}
}

type outputT struct {
	jsonToSend []byte
	msgToPrint string
	checkForFileChanges bool
}

type msgToMaster struct {
	guid string
	previousGuid string
	deletions []string
	creations []string
	allFiles []string
	directory string
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
			deletions: []string{},
			creations: []string{},
			previousGuid: previousGuid,
			guid: newestChange.guid,
			allFiles: getKeys(folder),
			directory: dirToWatch,
		}
	}
	result = msgToMaster{
		deletions: getKeys(newestChange.deletions),
		creations: getKeys(newestChange.creations),
		guid: newestChange.guid,
		previousGuid: previousGuid,
		allFiles: []string{},
		directory: dirToWatch,
	}
	return json.Marshal(result)
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
	fileEvents []fsnotify.Event
	fileErrors []error
	errChOk bool
	eventChOk bool
	requestErr error
	responseBody []byte
	readBodyErr error
	newGuid string
}

func initIoResult() ioResultT {
	return ioResultT{
		fileEvents: []fsnotify.Event{},
		fileErrors: []error{},
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

	fmt.Println("A")
	fmt.Println(string(output.jsonToSend))
	response, postErr := http.Post(
		masterUrl,
		"application/json",
		bytes.NewBuffer(output.jsonToSend))
	defer response.Body.Close()
	fmt.Println("C")
	result.requestErr = postErr
	if postErr != nil { return result }
	body, bodyErr := ioutil.ReadAll(response.Body)
	result.responseBody = body
	result.readBodyErr = bodyErr

	if !output.checkForFileChanges { return result }

	guidBytes, _ := uuid.NewV4()
	result.newGuid = guidBytes.String()

	watCh.ask <- true

	fmt.Println("D")
	events, eventsChOk := <-watCh.events
	fmt.Println("G")
	result.eventChOk = eventsChOk
	if !eventsChOk { return result }
	result.fileEvents = events

	fmt.Println("E")
	errorList, errChOk := <-watCh.errs
	result.errChOk = errChOk
	if !errChOk { return result }
	result.fileErrors = errorList

	fmt.Println("F")
	return result
}

func fatalErrors(fileErrors []error, eventChOk bool, errChOk bool) error {
	errSlice := fileErrors
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

func extractEvents(fileEvents []fsnotify.Event, toMatch map[fsnotify.Op]bool) map[string]bool {
	fileSet := map[string]bool{}
	for _, event := range fileEvents {
		_, relevantEvent := toMatch[event.Op]
		if relevantEvent {
			fileSet[event.Name] = true
		}
	}
	return fileSet
}

func deleteMap() map[fsnotify.Op]bool {
	d := map[fsnotify.Op]bool{}
	d[fsnotify.Remove] = true
	d[fsnotify.Rename] = true
	return d
}

func createMap() map[fsnotify.Op]bool {
	c := map[fsnotify.Op]bool{}
	c[fsnotify.Create] = true
	return c
}

func newChangeSet(fileEvents []fsnotify.Event, newGuid string) changeSet {
	return changeSet {
		guid: newGuid,
		deletions: extractEvents(fileEvents, deleteMap()),
		creations: extractEvents(fileEvents, createMap()),
	}
}

func update(state stateT, ioResult ioResultT) stateT {
	newChanges := newChangeSet(ioResult.fileEvents, ioResult.newGuid)
	return stateT{
		fatalError: fatalErrors(ioResult.fileErrors, ioResult.eventChOk, ioResult.errChOk),
		keepGoing: state.fatalError == nil,
		folder: updateFolder(state.folder, newChanges),
		lastChangeGuid: state.newestChange.guid,
		newestChange: newChanges,
		ignorantMaster: string(ioResult.responseBody) == "badGuid",
		nonFatalError: combineErrors([]error{ioResult.requestErr, ioResult.readBodyErr}),
	}
}

func updateFolder(folder map[string]bool, changes changeSet) map[string]bool {
	result := folder
	for fileToAdd, _ := range changes.creations {
		result[fileToAdd] = true
	}
	for fileToDelete, _ := range changes.deletions {
		delete(result, fileToDelete)
	}
	return result
}

type watcherChannelsT struct {
	events chan []fsnotify.Event
	errs chan []error
	ask chan bool
}

func main() {
	dirToWatch := "/home/true/toWatch"
	masterUrl := "http://localhost:3000"

	watCh := watcherChannelsT{
		events: make(chan []fsnotify.Event),
		errs: make(chan []error),
		ask: make(chan bool),
	}

	go func() {
		watcher, err := fsnotify.NewWatcher()
		watcher.Add(dirToWatch)
		if err != nil {
			watCh.errs <- []error{err}
			return
		}
		var events []fsnotify.Event
		var errorList []error
		for {
			select {
			case event := <-watcher.Events:
				events = append(events, event)
				fmt.Println("P")
			case err := <-watcher.Errors:
				errorList = append(errorList, err)
			case <-watCh.ask:
				fmt.Println("Y")
				fmt.Println(events)
				fmt.Println(errorList)
				if len(events) != 0 || len(errorList) != 0 {
					fmt.Println("X")
					watCh.events <- events
					watCh.errs <- errorList
					events = []fsnotify.Event{}
					errorList = []error{}
				}
			}
		}
	}()

	state := initState()
	for state.keepGoing {
		fmt.Println("B")
		state = update(state, io(watCh, stateToOutput(state, dirToWatch), masterUrl))
	}
}
