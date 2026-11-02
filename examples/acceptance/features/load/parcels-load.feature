@stormbreaker-group/delivery @stormbreaker-requires/parcels
Feature: The parcels service holds its load
  Load through the parcels service's front door, at a declared rate, open-model. The run declares
  every parcel it will register before any is sent; the parcels team's dispatcher prepares for
  them, and cleans them up after. The scenario passes only when that cleanup is verified.

  Scenario: 250 parcels at 50 a second
    Given the parcels service with the following properties:
      | url | ${sys:parcels.url} |
    And the parcels stream of POST requests to /parcels
    When the parcels stream is sent at 50 requests per second
    Then the load completes with no failed requests
    And the client's p99 latency of the load is at most 1 seconds

  # Downstream of parcels, tracking consumes every registration from Kafka: the run waits for it
  # to catch up, so that cleanup finds every tracking record the load caused.
  @lab
  Scenario: 250 parcels at 50 a second, downstream drained
    Given the parcels service with the following properties:
      | url | ${sys:parcels.url} |
    And the events kafka service with the following properties:
      | brokers | ${sys:kafka.brokers} |
    And the parcels stream of POST requests to /parcels
    When the parcels stream is sent at 50 requests per second
    Then the load completes with no failed requests
    And the tracking consumer group catches up on the parcel-events topic within 60s

  @lab
  Scenario: The service's own metrics agree, across both regions
    Given the parcels service with the following properties:
      | url | ${sys:parcels.url} |
    And the parcels-metrics prometheus service with the following properties:
      | url            | ${sys:prometheus.urls} |
      | selector       | job="parcels"          |
      | minimum-window | 10s                    |
    And the parcels stream of POST requests to /parcels
    When the parcels stream is sent at 25 requests per second
    And the run's traffic is complete
    Then within 30s the increase of http_server_requests_seconds_count over the run is at least 200
    And within 30s the p99 of http_server_requests_seconds over the run is at most 0.5 seconds
    And the events kafka service with the following properties:
      | brokers | ${sys:kafka.brokers} |
    And the tracking consumer group catches up on the parcel-events topic within 60s
