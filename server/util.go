package cloudlink

import (
	"fmt"

	"github.com/bwmarrin/snowflake"
)

// Gathers a map of all Snowflake IDs representing Clients in a Room or Manager.
func GatherSnowflakeIDs(clientstore any) map[any]*Client {
	allids := make(map[any]*Client)
	var readmode uint8
	var tmproom *Room
	var tmpmgr *Manager

	// Type assertions
	switch clientstore.(type) {
	case *Room:
		readmode = 1
	case *Manager:
		readmode = 2
	default:
		return nil
	}

	// Get type, lock, and then read clients
	var clients map[snowflake.ID]*Client
	switch readmode {
	case 1:
		tmproom = clientstore.(*Room)
		clients = tmproom.clients
	case 2:
		tmpmgr = clientstore.(*Manager)
		clients = tmpmgr.clients
	}

	// Gather Snowflake IDs
	for _, client := range clients {

		// Require a set username and a compatible protocol
		tmpclient := client
		if (tmpclient.username == nil) || (tmpclient.protocol != Protocol_CL4) {
			continue
		}

		allids[fmt.Sprint(client.id)] = client // Convert to strings for hash table searching
	}

	// Return collected Snowflake IDs as strings
	return allids
}

// Gathers a map of all UUIDs representing Clients in a Room or Manager.
func GatherUUIDs(clientstore any) map[any]*Client {
	alluuids := make(map[any]*Client)
	var readmode uint8
	var tmproom *Room
	var tmpmgr *Manager

	// Type assertions
	switch clientstore.(type) {
	case *Room:
		readmode = 1
	case *Manager:
		readmode = 2
	default:
		return nil
	}

	// Get type, lock, and then read clients
	var clients map[snowflake.ID]*Client
	switch readmode {
	case 1:
		tmproom = clientstore.(*Room)
		clients = tmproom.clients
	case 2:
		tmpmgr = clientstore.(*Manager)
		clients = tmpmgr.clients
	}

	// Gather UUIds
	for _, client := range clients {

		// Require a set username and a compatible protocol
		tmpclient := client
		if (tmpclient.username == nil) || (tmpclient.protocol != Protocol_CL4) {
			continue
		}

		alluuids[fmt.Sprint(client.uuid)] = client // Convert to strings for hash table searching
	}

	// Return collected UUIDs as strings
	return alluuids
}

// Gathers a map of all UserObjects representing Clients in a Room or Manager.
func GatherUserObjects(clientstore any) map[any]*Client {
	alluserobjects := make(map[any]*Client)
	var readmode uint8
	var tmproom *Room
	var tmpmgr *Manager

	// Type assertions
	switch clientstore.(type) {
	case *Room:
		readmode = 1
	case *Manager:
		readmode = 2
	default:
		return nil
	}

	// Get type, lock, and then read clients
	var clients map[snowflake.ID]*Client
	switch readmode {
	case 1:
		tmproom = clientstore.(*Room)
		clients = tmproom.clients
	case 2:
		tmpmgr = clientstore.(*Manager)
		clients = tmpmgr.clients
	}

	// Gather usernames
	for _, client := range clients {

		// Require a set username and a compatible protocol
		tmpclient := client
		if (tmpclient.username == nil) || (tmpclient.protocol != Protocol_CL4) {
			continue
		}

		alluserobjects[client.GenerateUserObject()] = client
	}

	// Return collected UserObjects
	return alluserobjects
}

// Gathers a map of all Usernames representing multiple Clients in a Room or Manager.
func GatherUsernames(clientstore any) map[any][]*Client {
	allusernames := make(map[any][]*Client)
	var readmode uint8
	var tmproom *Room
	var tmpmgr *Manager

	// Type assertions
	switch clientstore.(type) {
	case *Room:
		readmode = 1
	case *Manager:
		readmode = 2
	default:
		return nil
	}

	// Get type, lock, and then read clients
	var clients map[snowflake.ID]*Client
	switch readmode {
	case 1:
		tmproom = clientstore.(*Room)
		clients = tmproom.clients
	case 2:
		tmpmgr = clientstore.(*Manager)
		clients = tmpmgr.clients
	}

	// Gather usernames
	for _, client := range clients {

		// Require a set username and a compatible protocol
		tmpclient := client
		if (tmpclient.username == nil) || (tmpclient.protocol != Protocol_CL4) {
			continue
		}

		allusernames[client.username] = append(allusernames[client.username], client)
	}

	// Return collected usernames
	return allusernames
}

// Takes a UUID, Snowflake ID, Username, or UserObject query and returns either a single Client (UUID, Snowflake, UserObject) or multiple Clients (username).
func (room *Room) FindClient(query any) any {

	// TODO: fix this fugly slow mess
	switch query.(type) {

	// Handle hashtable-converted JSON types
	case map[string]any:
		// Attempt User object search
		userobjects := GatherUserObjects(room)
		querystring := query
		if _, ok := userobjects[querystring]; ok {
			return userobjects[querystring] // Returns *Client
		}

	// These two are expected to be strings
	case string:
		// Attempt Snowflake ID search
		snowflakeids := GatherSnowflakeIDs(room)
		if _, ok := snowflakeids[query]; ok {
			return snowflakeids[query] // Returns *Client
		}

		// Attempt UUID search
		uuids := GatherUUIDs(room)
		if _, ok := uuids[query]; ok {
			return uuids[query] // Returns *Client
		}
	}
	// BUG: Attempting to search for an entry of []any type crashes this
	// Attempt username search
	usernames := GatherUsernames(room)
	if _, ok := usernames[query]; ok {
		return usernames[query] // Returns array of *Client
	}

	// Unsupported type
	return nil
}

// Gathers all user objects in a room, and generates a userlist.
func (room *Room) GenerateUserList() []*UserObject {
	var objarray []*UserObject

	// Gather all UserObjects
	objstore := GatherUserObjects(room)

	// Convert to array
	for _, client := range objstore {
		objarray = append(objarray, client.GenerateUserObject())
	}

	return objarray
}

func RemoveValue(slice []any, indexRemove int) []any {

	// Swap the element to remove with the last element
	slice[indexRemove] = slice[len(slice)-1]

	// Remove the last element
	slice = slice[:len(slice)-1]
	return slice
}

func GetValue(slice []any, target any) int {
	for i, value := range slice {
		if value == target {
			return i
		}
	}
	return -1 // Indicates that the value was not found
}
