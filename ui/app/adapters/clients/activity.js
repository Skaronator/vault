import Application from '../application';
import { formatRFC3339 } from 'date-fns';

export default Application.extend({
  formatTimeParams(query) {
    let { start_time, end_time } = query;
    // do not query without start_time. Otherwise returns last year data, which is not reflective of billing data.
    if (start_time) {
      // check if it's an array, if it is, it's coming from an action like selecting a new startTime or new EndTime
      if (Array.isArray(start_time)) {
        let startYear = Number(start_time[0]);
        let startMonth = Number(start_time[1]);
        start_time = formatRFC3339(new Date(startYear, startMonth));
      }
      if (end_time) {
        if (Array.isArray(end_time)) {
          let endYear = Number(end_time[0]);
          let endMonth = Number(end_time[1]);
          end_time = formatRFC3339(new Date(endYear, endMonth));
        }

        return { start_time, end_time };
      } else {
        return { start_time };
      }
    } else {
      // did not have a start time, return null through to component.
      return null;
    }
  },

  // ARG TODO current Month tab is hitting this endpoint. Need to amend so only hit on Monthly history (large payload)
  // query comes in as either: {start_time: '2021-03-17T00:00:00Z'} or
  // {start_time: Array(2), end_time: Array(2)}
  // end_time: (2) ['2022', 0]
  // start_time: (2) ['2021', 2]
  queryRecord(store, type, query) {
    let url = `${this.buildURL()}/internal/counters/activity`;
    // check if start and/or end times are in RFC3395 format, if not convert with timezone UTC/zulu.
    let queryParams = this.formatTimeParams(query);
    if (queryParams) {
      return this.ajax(url, 'GET', { data: queryParams }).then((resp) => {
        let response = resp || {};
        response.id = response.request_id || 'no-data';
        return response;
      });
    } else {
      // did not have a start time, return null through to component.
      return null;
    }
  },
});
