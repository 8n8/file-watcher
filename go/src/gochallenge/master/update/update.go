package update

import (
	"sort"
	"encoding/json"
	"strings"
)

func handleWatcherInput(state stateT, ioResult IoResultT) stateT {
	if ioResult.RawWatcherInput.BodyReadErr != nil {
		return stateT{
			FolderFileSets: state.FolderFileSets,
			MasterList: state.MasterList,
			NonFatalErrs: []error{ioResult.RawWatcherInput.BodyReadErr},
			MsgToWatcher: MsgToWatcherT{
				Msg: []byte("internal error"),
				Ch: ioResult.RawWatcherInput.ReplyChan,
			},
		}
	}
	watcherInput, err := decodeInput(ioResult.RawWatcherInput.Content)
	if err != nil {
		return stateT{
			FolderFileSets: state.FolderFileSets,
			MasterList: state.MasterList,
			NonFatalErrs: []error{err},
			MsgToWatcher: MsgToWatcherT{
				Msg: []byte("could not decode json"),
				Ch: ioResult.RawWatcherInput.ReplyChan,
			},
		}
	}
	if watcherInput.NewList {
		fileSets := state.FolderFileSets
		fileSets[watcherInput.Directory] = newFolderState(
			watcherInput.AllFiles,
			watcherInput.Guid)
		return stateT{
			FolderFileSets: fileSets,
			MasterList: makeMasterList(fileSets),
			NonFatalErrs: []error{},
			MsgToWatcher: MsgToWatcherT{
				Msg: []byte("ok"),
				Ch: ioResult.RawWatcherInput.ReplyChan,
			},
		}
	}
	if state.FolderFileSets[watcherInput.Directory].guidOfLastUpdate != watcherInput.PreviousGuid {
		return stateT {
			FolderFileSets: state.FolderFileSets,
			MasterList: state.MasterList,
			NonFatalErrs: []error{},
			MsgToWatcher: MsgToWatcherT{
				Msg: []byte("badGuid"),
				Ch: ioResult.RawWatcherInput.ReplyChan,
			},
		}
	}
	fileSets := state.FolderFileSets
	fileSets[watcherInput.Directory] = updateFolderState(
		state.FolderFileSets[watcherInput.Directory].fileSet,
		watcherInput)
	return stateT{
		FolderFileSets: fileSets,
		MasterList: makeMasterList(fileSets),
		NonFatalErrs: []error{},
		MsgToWatcher: MsgToWatcherT{
			Msg: []byte("ok"),
			Ch: ioResult.RawWatcherInput.ReplyChan,
		},
	}
}

func updateFolderState(current map[string]bool, watcherInput watcherInputT) folderState {
	newFileSet := current
	if watcherInput.ChangeType == "create" {
		newFileSet[watcherInput.FileName] = true
	}
	if watcherInput.ChangeType == "delete" {
		delete(newFileSet, watcherInput.FileName)
	}
	return folderState{
		fileSet: newFileSet,
		guidOfLastUpdate: watcherInput.Guid,
	}
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

func makeMasterList(folderFileSets map[string]folderState) []string {
	all := map[string]bool{}
	for _, v := range folderFileSets {
		for k, _ := range v.fileSet {
			all[k] = true
		}
	}
	asSlice := getKeys(all)
	sort.Strings(asSlice)
	return asSlice
}

func newFolderState(fileList []string, updateGuid string) folderState {
	fileSet := map[string]bool{}
	for _, filename := range fileList {
		fileSet[filename] = true
	}
	return folderState{
		fileSet: fileSet,
		guidOfLastUpdate: updateGuid,
	}
}

func Update(state stateT, ioResult IoResultT) stateT {
	if ioResult.ClientOrWatcher == WatcherInputC {
		return handleWatcherInput(state, ioResult)
	}
	result := state
	result.RespondToClient = true
	result.MsgToWatcher = MsgToWatcherT{
		Msg: []byte{},
		Ch: make(chan []byte),
	}
	result.ClientChan = ioResult.ClientRequest
	return result
}

type stateT struct {
	FolderFileSets map[string]folderState
	MasterList []string
	NonFatalErrs []error
	MsgToWatcher MsgToWatcherT
	RespondToClient bool
	ClientChan chan []byte
}

type IoResultT struct {
	RawWatcherInput RawWatcherInputT
	ClientRequest chan []byte
	ClientOrWatcher clientOrWatcherT
}

type RawWatcherInputT struct {
	Content []byte
	ReplyChan chan []byte
	BodyReadErr error
}

type clientOrWatcherT bool

const (
	ClientRequestC clientOrWatcherT = false
	WatcherInputC clientOrWatcherT = true
)

func InitState() stateT {
	return stateT{
		FolderFileSets: map[string]folderState{},
		MasterList:     []string{},
		NonFatalErrs: []error{},
		RespondToClient: false,
		ClientChan: nil,
	}
}

type MsgToWatcherT struct {
	Msg []byte
	Ch chan []byte
}

type folderState struct {
	fileSet map[string]bool
	guidOfLastUpdate string
}

type watcherInputT struct {
	Guid string
	PreviousGuid string
	FileName string
	ChangeType string
	AllFiles []string
	Directory string
	NewList bool
}

func decodeInput(raw []byte) (watcherInputT, error) {
	var watcherInput watcherInputT
	err := json.Unmarshal(raw, &watcherInput)
	return watcherInput, err
}

func StateToOutput(state stateT) OutputT {
	var result OutputT
	nonFatalErrs := state.NonFatalErrs
	if state.RespondToClient {
		msg, err := json.Marshal(state.MasterList)
		nonFatalErrs = append(nonFatalErrs, err)
		if err == nil {
			result.ResponseToClient.Content = msg
			result.ResponseToClient.Ch = state.ClientChan
		}
	}
	result.ResponseToWatcher = state.MsgToWatcher
	result.MsgToPrint = combineErrors(nonFatalErrs)
	return result
}

func combineErrors(errorList []error) string {
	var noNils []error
	for _, err := range errorList {
		if err != nil {
			noNils = append(noNils, err)
		}
	}
	if len(noNils) == 0 { return "" }
	return strings.Join(errorsToStrings(noNils), "\n")
}

func errorsToStrings(errors []error) []string {
	var strs []string
	for _, err := range errors {
		strs = append(strs, err.Error())
	}
	return strs
}

type OutputT struct {
	ResponseToClient ResponseToClientT
	ResponseToWatcher MsgToWatcherT
	MsgToPrint string
}

type ResponseToClientT struct {
	Content []byte
	Ch chan []byte
}
