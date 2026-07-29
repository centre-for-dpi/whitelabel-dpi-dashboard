package apispec

// What each message and field means, for a reader holding an HTTP client.
//
// The .proto files carry comments of their own, and this is not a copy of them.
// Those are addressed to someone reading a schema definition; these are addressed
// to someone about to send a request, so they say what a value must be, what
// units it is in, and what happens when it is wrong — including the several
// cases where the answer is "the dashboard ignores what you sent and works it
// out itself".
//
// Keys are the message's full proto name, optionally followed by the field's
// JSON name. A test asserts this table and the descriptors agree in both
// directions: a field with no entry fails, and an entry naming no field fails
// too, because a renamed field otherwise leaves its documentation behind
// describing something that no longer exists.
func descriptions() map[string]string {
	return map[string]string{
		// --- what a deployment sends ---------------------------------------

		"dpi.v1.IngestRequest": "One batch of observations. Send as JSON or as binary protobuf; " +
			"the endpoint decides on `Content-Type` and defaults to JSON, so a collector that " +
			"sets none at all still works.",
		"dpi.v1.IngestRequest.mode": "Whether this payload adds to what the dashboard holds or replaces it. " +
			"Omitted, it upserts.",
		"dpi.v1.IngestRequest.services": "The observations. An empty list is accepted and changes nothing " +
			"in upsert mode; in replace mode it empties the dashboard.",
		"dpi.v1.IngestRequest.sourceId": "Free-form label for the collector, so several feeding one " +
			"dashboard stay distinguishable in its logs.",

		"dpi.v1.IngestService": "One service as its operator observed it. Deliberately smaller than the " +
			"record the read API returns: status, trends and rank movement are absent rather than " +
			"ignored, because a deployment reports observations and the dashboard decides verdicts.",
		"dpi.v1.IngestService.id": "Stable identifier, and the key an upsert merges on. Required — a " +
			"record without one is rejected and the rest of the batch is still stored.",
		"dpi.v1.IngestService.key": "Short slug. A requestor's `callsKey` names the issuer it calls by " +
			"this, so an issuer other records point at needs one.",
		"dpi.v1.IngestService.nameTermId": "Term id resolved against the reader's locale, e.g. " +
			"`svc.aadhaar.name`. A deployment that does not translate can send literal text: the " +
			"resolver falls through to the raw string when no key matches.",
		"dpi.v1.IngestService.descTermId": "Term id for the one-line description, resolved like " +
			"`nameTermId`.",
		"dpi.v1.IngestService.categoryId": "Must be one of the categories declared in the deployment's " +
			"`domain.yaml`, e.g. `cat.identity`. An undeclared value rejects the record, because a " +
			"category no filter offers is a service no reader can find.",
		"dpi.v1.IngestService.regionId": "Must be one of the regions declared in `domain.yaml`, e.g. " +
			"`reg.national`. Validated like `categoryId`.",
		"dpi.v1.IngestService.providerId": "Term id for the operating body, e.g. `prov.uidai`. Not " +
			"validated against the taxonomy.",
		"dpi.v1.IngestService.scope": "Which view the service belongs to — the deployment's declared " +
			"scopes, `national` or `state` as shipped. Validated; an undeclared value rejects the record.",
		"dpi.v1.IngestService.metrics": "The current reading. What the status is derived from.",
		"dpi.v1.IngestService.maintenance": "Planned work in progress, which suppresses the outage " +
			"verdict for its duration.",
		"dpi.v1.IngestService.history": "Sealed daily buckets, oldest first. Optional: supply them to " +
			"backfill the charts immediately, or omit them and the dashboard rolls its own up from " +
			"successive observations.",
		"dpi.v1.IngestService.incidents": "Open and recent incidents, with their timeline. There is no " +
			"way to express these through a pull mapping, so a deployment that wants them uses push.",
		"dpi.v1.IngestService.errors": "The error codes behind the error rate, as buckets. Like " +
			"incidents, reachable only through push.",
		"dpi.v1.IngestService.observedAt": "When the upstream took this reading. Absent, the server " +
			"stamps its own receipt time — which is a different claim, so send it if you know it.",
		"dpi.v1.IngestService.roleId": "Which side of the exchange this is: `issuer` for a service that " +
			"publishes a document, `requestor` for an organisation that consumes one. Empty means the " +
			"deployment's default role.",
		"dpi.v1.IngestService.sectorId": "For a requestor, the sector running the use case, e.g. " +
			"`sec.banking`. Empty for an issuer, which consumes nothing.",
		"dpi.v1.IngestService.callsKey": "For a requestor, the `key` of the issuer it calls. This is what " +
			"builds the demand side: who pulls what, and how much of it.",
		"dpi.v1.IngestService.subscriptionType": "How a requestor is subscribed to the API it calls, e.g. " +
			"`document` or `service`.",
		"dpi.v1.IngestService.ownErrorShare": "Percent, 0–100, of a requestor's errors that are its own " +
			"rather than the publisher's — a 4xx it sent, not a 5xx it received. A requestor told only " +
			"its error rate has a number it cannot act on.",

		"dpi.v1.IngestResponse": "What was stored, and what was not. Always 200 when the body decoded: a " +
			"malformed record in a batch of two hundred does not reject the other hundred and " +
			"ninety-nine.",
		"dpi.v1.IngestResponse.accepted":   "How many records were stored. Omitted when none were.",
		"dpi.v1.IngestResponse.rejected":   "How many records were dropped. Omitted when none were.",
		"dpi.v1.IngestResponse.errors":     "Why each dropped record was dropped.",
		"dpi.v1.IngestResponse.receivedAt": "The server's clock when it handled the request.",

		"dpi.v1.IngestError":           "One reason one record was dropped. A single record can produce several.",
		"dpi.v1.IngestError.serviceId": "The `id` of the record concerned, empty if that was what was missing.",
		"dpi.v1.IngestError.field":     "Which field, as a dotted JSON path — `metrics.availability`, `scope`.",
		"dpi.v1.IngestError.message":   "What was wrong with it, in plain words.",

		"dpi.v1.IngestMode": "How this payload relates to what the dashboard already holds. " +
			"`INGEST_MODE_UNSPECIFIED` is treated as an upsert.",

		// --- what the dashboard returns -------------------------------------

		"dpi.v1.ListServicesResponse":             "Everything the dashboard currently holds.",
		"dpi.v1.ListServicesResponse.services":    "One record per service, in the order the source supplied them.",
		"dpi.v1.ListServicesResponse.generatedAt": "When this snapshot was assembled.",
		"dpi.v1.Service":                          "One service as the dashboard holds it: what was reported, and what it concluded.",
		"dpi.v1.Service.id":                       "Stable identifier.",
		"dpi.v1.Service.key":                      "Short slug. A requestor's `callsKey` matches on this.",
		"dpi.v1.Service.nameTermId":               "Term id for the display name.",
		"dpi.v1.Service.descTermId":               "Term id for the one-line description.",
		"dpi.v1.Service.categoryId":               "A category declared in `domain.yaml`.",
		"dpi.v1.Service.regionId":                 "A region declared in `domain.yaml`.",
		"dpi.v1.Service.providerId":               "Term id for the operating body.",
		"dpi.v1.Service.scope":                    "Which view the service belongs to.",
		"dpi.v1.Service.status": "**Derived, output only.** Recomputed on every snapshot from `metrics` " +
			"against the thresholds the deployment publishes, so the rule shown on screen is provably " +
			"the rule that was applied. Sending one has no effect; the ingest message has no field for it.",
		"dpi.v1.Service.metrics":     "The current reading.",
		"dpi.v1.Service.maintenance": "Planned work in progress.",
		"dpi.v1.Service.history":     "Sealed daily buckets, oldest first.",
		"dpi.v1.Service.incidents":   "Open and recent incidents.",
		"dpi.v1.Service.errors":      "The error codes behind the error rate.",
		"dpi.v1.Service.trends": "**Derived, output only.** Keyed by metric: `availability`, `errorRate`, " +
			"`latencyP50`, `volume`.",
		"dpi.v1.Service.rankMovement":     "**Derived, output only.** Places moved since the previous snapshot.",
		"dpi.v1.Service.observedAt":       "When the reading was taken.",
		"dpi.v1.Service.roleId":           "`issuer` or `requestor`.",
		"dpi.v1.Service.sectorId":         "For a requestor, the sector running the use case.",
		"dpi.v1.Service.callsKey":         "For a requestor, the `key` of the issuer it calls.",
		"dpi.v1.Service.subscriptionType": "How a requestor is subscribed to the API it calls.",
		"dpi.v1.Service.ownErrorShare":    "Percent of a requestor's errors that are its own.",

		// --- the shared parts ------------------------------------------------

		"dpi.v1.Metrics": "One reading of a service.",
		"dpi.v1.Metrics.availability": "Percent, 0–100 — `99.92`, not `0.9992`. **Absent is not zero.** " +
			"A missing reading yields `STATUS_UNKNOWN`; a zero says the service is wholly down. A value " +
			"between 0 and 1 is accepted, because a genuine 0.9% availability has to stay reportable, " +
			"but it is logged as a probable ratio-for-percent mistake.",
		"dpi.v1.Metrics.errorRate":    "Percent, 0–100, of requests that failed.",
		"dpi.v1.Metrics.latencyP50Ms": "Median response time in milliseconds.",
		"dpi.v1.Metrics.staleSeconds": "How old the reading is. Past the deployment's " +
			"`staleSecondsAbove` threshold the status becomes `STATUS_UNKNOWN`, because a stale " +
			"reading is not evidence that anything is working.",
		"dpi.v1.Metrics.volume":     "Request counts over the reporting window.",
		"dpi.v1.Volume":             "Request counts over the reporting window.",
		"dpi.v1.Volume.total":       "Requests received.",
		"dpi.v1.Volume.success":     "Requests that succeeded. More successes than requests rejects the record.",
		"dpi.v1.Maintenance":        "Planned work, which suppresses the outage verdict while it is in progress.",
		"dpi.v1.Maintenance.active": "Whether work is in progress now.",
		"dpi.v1.Maintenance.until":  "When it is expected to end.",
		"dpi.v1.Maintenance.reasonTermId": "Term id for the reason shown to a reader, e.g. " +
			"`maint.scheduled`.",

		"dpi.v1.HistoryPoint":              "One sealed daily bucket.",
		"dpi.v1.HistoryPoint.day":          "UTC midnight of the day this bucket covers.",
		"dpi.v1.HistoryPoint.availability": "Percent, 0–100, over the day. Absent is not zero.",
		"dpi.v1.HistoryPoint.errorRate":    "Percent, 0–100, over the day.",
		"dpi.v1.HistoryPoint.latencyP50Ms": "Median response time over the day, in milliseconds.",
		"dpi.v1.HistoryPoint.volume":       "Requests over the day.",
		"dpi.v1.HistoryPoint.samples": "How many raw readings were folded into this bucket. Zero for a " +
			"bucket supplied whole by an upstream rather than rolled up locally.",

		"dpi.v1.Incident":            "One interruption, with the timeline of what was done about it.",
		"dpi.v1.Incident.id":         "Stable identifier for the incident.",
		"dpi.v1.Incident.serviceId":  "The service it concerns.",
		"dpi.v1.Incident.severity":   "Only `STATUS_MAJOR` and `STATUS_PARTIAL` are meaningful here.",
		"dpi.v1.Incident.openedAt":   "When it began.",
		"dpi.v1.Incident.closedAt":   "When it ended. Absent while it is still open.",
		"dpi.v1.Incident.open":       "Whether it is still open.",
		"dpi.v1.Incident.noteTermId": "Term id for the one-line summary shown to a reader.",
		"dpi.v1.Incident.events":     "What happened and when, oldest first.",

		"dpi.v1.IncidentEvent": "One stage of an incident.",
		"dpi.v1.IncidentEvent.type": "Free-form, so a deployment can model its own lifecycle. Displayed " +
			"through the term id `inc.<type>`, which means adding a stage is a locale key rather than a " +
			"contract change. Well-known values: `opened`, `acknowledged`, `mitigated`, `resolved`.",
		"dpi.v1.IncidentEvent.at": "When the stage was reached.",

		"dpi.v1.ErrorBucket":        "One error code, and how much of the failure it accounts for.",
		"dpi.v1.ErrorBucket.code":   "The code as reported, e.g. `500`, `429`, `timeout`.",
		"dpi.v1.ErrorBucket.termId": "Term id for the human explanation, e.g. `err.500`.",
		"dpi.v1.ErrorBucket.class":  "Whose failure it is.",
		"dpi.v1.ErrorBucket.count":  "How many requests failed this way.",
		"dpi.v1.ErrorBucket.share":  "Percent, 0–100, of this service's errors.",
		"dpi.v1.ErrorBucket.trend":  "Which way the count is moving.",
		"dpi.v1.Trend":              "**Derived, output only.** How a metric has moved over the trend window.",
		"dpi.v1.Trend.delta":        "The change, in the metric's own units.",
		"dpi.v1.Trend.direction":    "Which way it moved. Better or worse depends on the metric.",
		"dpi.v1.Trend.periodDays":   "How many days the comparison covers.",

		"dpi.v1.Status": "The verdict. A fixed vocabulary, not a configurable one: the threshold rules " +
			"are tied to these five outcomes, so a sixth would be a contract change. Config controls " +
			"each one's label, glyph, colour and thresholds.",
		"dpi.v1.TrendDirection": "Which way a figure moved. Not whether that is good news — for " +
			"availability up is better, for error rate it is worse.",
		"dpi.v1.ErrorClass": "`SERVER` is a 5xx, `CLIENT` a 4xx, `NETWORK` a timeout, reset or DNS " +
			"failure — which is the distinction between a fault the operator can fix and one the " +
			"caller can.",
	}
}
