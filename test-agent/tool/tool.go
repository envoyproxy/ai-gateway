package tool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"
)

// Tool represents a tool that can be called by the agent.
type Tool struct {
	Name        string
	Description string
}

// WeatherInput represents the input structure for the weather prediction tool.
type WeatherInput struct {
	City string `json:"city"`
}

// WeatherOutput represents the output structure for the weather prediction tool.
type WeatherOutput struct {
	TempMax float64 `json:"temp_max"`
}

// CallTool executes the tool with the given parameters.
func CallTool(toolName string, parameters map[string]string) (string, error) {
	switch toolName {
	case "get_weather_prediction":
		return getWeatherPrediction(parameters)
	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}

// getWeatherPrediction predicts the weather for tomorrow in a specific city.
func getWeatherPrediction(parameters map[string]string) (string, error) {
	city, ok := parameters["city"]
	if !ok {
		return "", fmt.Errorf("missing parameter: city")
	}

	last10Days := getLast10Days(city)
	requestBody, err := json.Marshal(map[string]interface{}{
		"inputs": []map[string]interface{}{
			{
				"name":     "input.1",
				"shape":    []int{1, 10, 4},
				"datatype": "FP32",
				"data":     last10Days,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal request body: %w", err)
	}

	// todo URL
	resp, err := http.Post("{{URL}}v2/models/weather-predictor/infer", "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to call weather prediction API: %w", err)
	}
	defer resp.Body.Close()

	var respBody map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return "", fmt.Errorf("failed to decode response body: %w", err)
	}

	output := WeatherOutput{
		TempMax: respBody["outputs"].([]interface{})[0].(map[string]interface{})["data"].([]interface{})[0].(float64),
	}
	outputBytes, err := json.Marshal(output)
	if err != nil {
		return "", fmt.Errorf("failed to marshal output: %w", err)
	}

	return string(outputBytes), nil
}

// getLast10Days generates random weather data for the last 10 days for a specific city.
func getLast10Days(city string) [][]float64 {
	consts := map[string]float64{
		"San Diego": 22,
		"London":    10,
		"New York":  9,
		"Montreal":  -10,
	}

	temp := rand.Float64() * consts[city]
	last10 := make([][]float64, 10)
	for i := 0; i < 10; i++ {
		precipitation := rand.Float64() * 10.0
		maxTemp := temp + (rand.Float64()*5 - 2.5)
		minTemp := maxTemp - rand.Float64()*8
		wind := rand.Float64() * 8
		temp = maxTemp
		last10[i] = []float64{precipitation, maxTemp, minTemp, wind}
	}

	return last10
}
