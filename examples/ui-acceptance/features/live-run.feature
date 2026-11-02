@stormbreaker-group/delivery @stormbreaker-requires/parcels
Feature: The UI follows a run as it goes
  While a scenario's run is open, the UI shows it as it goes: the scenario's own steps, the load
  it sends and how that load fares, the run's gate, each instance's part, and the journal (ADR
  0027). Each scenario here watches its own run: it opens one, declares 250 parcels, sends them,
  and looks.

  Background:
    Given the stormbreaker web app with the following properties:
      | url | ${sys:ui.url} |
    And the parcels service with the following properties:
      | url | ${sys:parcels.url} |
    And the parcels stream of POST requests to /parcels

  Scenario: The environment shows the run as it goes, in its scenario's own words
    When the parcels stream is sent at 50 requests per second
    And the "/" page is opened
    Then within 10s the page shows "The environment shows the run as it goes, in its scenario's own words"
    And within 10s the page shows "of 250 sent"
    And the page shows "gate open"

  Scenario: A run's page shows its steps, its load, its gate and its journal
    When the parcels stream is sent at 50 requests per second
    And the "/" page is opened
    And the "Follow A run's page shows its steps, its load, its gate and its journal" link is clicked
    Then within 10s the page shows "Records a second"
    And the page shows "When the parcels stream is sent at 50 requests per second"
    And within 10s the page shows "declared 50/s"
    And the page shows "parcels/east"
    And within 10s the page shows "declares 250 identities at parcels:/parcels"
