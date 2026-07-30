// Package api will implement the versioned (/v1) HTTP query API that
// consumers such as hass-smartmeter use to read readings without knowing
// which provider or store backend produced them:
//
//	GET /v1/points
//	GET /v1/readings?point=<id>&since=<RFC3339>
//
// Not yet implemented.
package api
