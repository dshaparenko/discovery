package common

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	toolsRender "github.com/devopsext/tools/render"
	"github.com/devopsext/utils"
)

type TelegrafInputPrometheusHttpFile struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
	Type string `toml:"type,omitempty"`
}

type TelegrafInputPrometheusHttpMetric struct {
	Name     string            `toml:"name"`
	Query    string            `toml:"query"`
	UniqueBy []string          `toml:"unique_by,omitempty"`
	Tags     map[string]string `toml:"tags,omitempty"`
}

type TelegrafInputPrometheusHttpAvailability struct {
	Name     string            `toml:"name"`
	Query    string            `toml:"query"`
	UniqueBy []string          `toml:"unique_by,omitempty"`
	Tags     map[string]string `toml:"tags,omitempty"`
}

type TelegrafInputPrometheusHttp struct {
	Name          string                                     `toml:"name"`
	URL           string                                     `toml:"url"`
	Version       string                                     `toml:"version"`
	Params        string                                     `toml:"params"`
	Interval      string                                     `toml:"interval"`
	Timeout       string                                     `toml:"timeout"`
	Duration      string                                     `toml:"duration"`
	Prefix        string                                     `toml:"prefix"`
	File          []*TelegrafInputPrometheusHttpFile         `toml:"file"`
	Metric        []*TelegrafInputPrometheusHttpMetric       `toml:"metric"`
	Availability  []*TelegrafInputPrometheusHttpAvailability `toml:"metric"`
	Tags          map[string]string                          `toml:"tags,omitempty"`
	Include       []string                                   `toml:"taginclude,omitempty"`
	SkipEmptyTags bool                                       `toml:"skip_empty_tags"`
}

type TelegrafInputs struct {
	PrometheusHttp []*TelegrafInputPrometheusHttp `toml:"prometheus_http,omitempty"`
}

type TelegrafConfigOptions struct {
	URL              string
	Version          string
	Params           string
	Interval         string
	Timeout          string
	Duration         string
	Prefix           string
	QualityName      string
	QualityRange     string
	QualityEvery     string
	QualityPoints    int
	QualityQuery     string
	AvailabilityName string
	MetricName       string
	DefaultTags      []string
	VarFormat        string
}

type TelegrafConfig struct {
	Inputs TelegrafInputs `toml:"inputs"`
}

//[[inputs.prometheus_http]]
//  [inputs.prometheus_http.tags]
//  [[inputs.prometheus_http.file]]
//  [[inputs.prometheus_http.metric]]
//    [inputs.prometheus_http.metric.tags]

func (ti *TelegrafInputPrometheusHttp) updateIncludeTags(tags []string) {

	for _, tag := range tags {
		if !StringInArr(tag, ti.Include) {
			ti.Include = append(ti.Include, tag)
		}
	}
}

func (ti *TelegrafInputPrometheusHttp) sanitizeQuery(query string) string {

	res := strings.ReplaceAll(query, "\"", "'")
	res = strings.ReplaceAll(res, "\n", "")
	return res
}

func (ti *TelegrafInputPrometheusHttp) setVars(q, f string, vars map[string]string) string {
	for k, v := range vars {
		q = strings.ReplaceAll(q, fmt.Sprintf(f, k), v)
	}
	return q
}

func (ti *TelegrafInputPrometheusHttp) render(def string, obj interface{}) string {

	tpl, err := toolsRender.NewTextTemplate(toolsRender.TemplateOptions{Content: def}, nil)
	if err != nil {
		return def
	}

	s, err := RenderTemplate(tpl, def, obj)
	if err != nil {
		return def
	}
	return s
}

func (ti *TelegrafInputPrometheusHttp) renderLabels(tpl string, tags map[string]string, vars map[string]string) map[string]string {

	m := make(map[string]interface{})
	m["tags"] = tags
	m["vars"] = vars
	s := ti.render(tpl, m)
	kv := utils.MapGetKeyValues(s)

	return MergeMaps(tags, kv)
}

func (ti *TelegrafInputPrometheusHttp) enableLabel(name, l string) string {

	if l == "" {
		return l
	}

	arr := strings.Split(l, "=")
	if len(arr) == 0 {
		return l
	}

	if len(arr) == 1 {
		return arr[0]
	}

	match, _ := regexp.MatchString(arr[1], name)
	if match {
		return arr[0]
	}
	return ""
}

func (ti *TelegrafInputPrometheusHttp) buildTags(name string, labels map[string]string, f string, vars map[string]string) map[string]string {

	r := make(map[string]string)

	for k, l := range labels {
		lnew := ti.enableLabel(name, l)
		if utils.IsEmpty(lnew) {
			continue
		}
		r[k] = ti.setVars(lnew, f, vars)
	}
	return r
}

func (ti *TelegrafInputPrometheusHttp) buildQualities(qualities []*BaseQuality, tpl string, opts TelegrafConfigOptions, labels map[string]string, vars map[string]string) {

	metric := &TelegrafInputPrometheusHttpMetric{}
	metric.Name = opts.QualityName
	var queries []string

	for _, v := range qualities {

		if utils.IsEmpty(strings.TrimSpace(v.Query)) {
			continue
		}

		bq := &BaseQuality{
			Range:  IfDef(v.Range, opts.QualityRange).(string),
			Every:  IfDef(v.Every, opts.QualityEvery).(string),
			Points: IfDef(v.Points, opts.QualityPoints).(int),
			Query:  ti.setVars(v.Query, opts.VarFormat, vars),
		}

		qe := ti.render(opts.QualityQuery, bq)
		qe = ti.sanitizeQuery(qe)
		queries = append(queries, qe)
	}

	metric.Query = fmt.Sprintf("(%s)/%d", strings.Join(queries, " + "), len(queries))
	tags := ti.buildTags(metric.Name, labels, opts.VarFormat, vars)
	tags = ti.renderLabels(tpl, tags, vars)

	keys := GetStringKeys(tags)
	sort.Strings(keys)
	ti.updateIncludeTags(keys)
	metric.Tags = tags
	ti.Metric = append(ti.Metric, metric)
}

func (ti *TelegrafInputPrometheusHttp) buildAvailability(baseAvailability *BaseAvailability, tpl string, opts TelegrafConfigOptions, labels map[string]string, vars map[string]string) {

	if baseAvailability != nil {

		for _, a := range baseAvailability.Queries {

			availability := &TelegrafInputPrometheusHttpAvailability{}

			if a.Suffix != "" {
				availability.Name = fmt.Sprintf("%s:%s", opts.AvailabilityName, a.Suffix)
			} else {
				availability.Name = opts.AvailabilityName
			}

			qe := ti.setVars(a.Query, opts.VarFormat, vars)
			availability.Query = ti.sanitizeQuery(qe)
			tags1 := ti.buildTags(availability.Name, labels, opts.VarFormat, vars)
			tags2 := ti.buildTags(availability.Name, a.Labels, opts.VarFormat, vars)
			tags := MergeMaps(tags1, tags2)
			tags = ti.renderLabels(tpl, tags, vars)
			keys := GetStringKeys(tags)
			sort.Strings(keys)
			ti.updateIncludeTags(keys)
			availability.Tags = tags
			ti.Availability = append(ti.Availability, availability)
		}
	}

}

func (ti *TelegrafInputPrometheusHttp) buildMetrics(metrics []*BaseMetric, tpl string, opts TelegrafConfigOptions, labels map[string]string, vars map[string]string) {

	for _, m := range metrics {

		metric := &TelegrafInputPrometheusHttpMetric{}
		metric.Name = IfDef(m.Name, opts.MetricName).(string)

		qe := ti.setVars(m.Query, opts.VarFormat, vars)
		metric.Query = ti.sanitizeQuery(qe)
		metric.UniqueBy = m.UniqueBy
		tags1 := ti.buildTags(metric.Name, labels, opts.VarFormat, vars)
		tags2 := ti.buildTags(metric.Name, m.Labels, opts.VarFormat, vars)
		tags := MergeMaps(tags1, tags2)
		tags = ti.renderLabels(tpl, tags, vars)

		keys := GetStringKeys(tags)
		sort.Strings(keys)
		ti.updateIncludeTags(keys)
		metric.Tags = tags
		ti.Metric = append(ti.Metric, metric)
	}
}

func (tc *TelegrafConfig) GenerateServiceBytes(s *Service, labelsTpl, filesTpl string, opts TelegrafConfigOptions, name string) ([]byte, error) {

	input := &TelegrafInputPrometheusHttp{}
	input.Name = name
	input.URL = opts.URL
	input.Version = opts.Version
	input.Params = opts.Params
	input.Interval = opts.Interval
	input.Timeout = opts.Timeout
	input.Duration = opts.Duration
	input.Prefix = opts.Prefix
	input.Tags = make(map[string]string)
	input.SkipEmptyTags = true

	keys := GetBaseConfigKeys(s.Configs)
	sort.Strings(keys)

	for _, k := range keys {

		c := s.Configs[k]
		labels := MergeMaps(c.Labels, s.Labels)
		vars := MergeMaps(c.Vars, s.Vars)

		input.buildQualities(c.Qualities, labelsTpl, opts, labels, vars)
		input.buildAvailability(c.Availability, labelsTpl, opts, labels, vars)
		input.buildMetrics(c.Metrics, labelsTpl, opts, labels, vars)
	}
	input.updateIncludeTags(opts.DefaultTags)
	sort.Strings(input.Include)

	fs := input.render(filesTpl, s.Vars)
	kv := utils.MapGetKeyValues(fs)
	for k, v := range kv {
		if !utils.IsEmpty(v) {
			input.File = append(input.File, &TelegrafInputPrometheusHttpFile{
				Name: k,
				Path: v,
			})
		}
	}

	tc.Inputs.PrometheusHttp = append(tc.Inputs.PrometheusHttp, input)

	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	if err := toml.NewEncoder(w).Encode(tc); err != nil {
		return nil, err
	}

	return b.Bytes(), nil
}
