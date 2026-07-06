import os
import yaml
import json  # <-- Import json
import boto3
from flask import Flask, jsonify, request, render_template_string
from botocore.exceptions import ClientError, NoCredentialsError
from botocore.client import Config

# --- Configuration ---
# We read these from environment variables.
# The browser NEVER sees these.
S3_BUCKET_NAME = os.environ.get('S3_BUCKET_NAME')
S3_ACCESS_KEY = os.environ.get('S3_ACCESS_KEY')
S3_SECRET_KEY = os.environ.get('S3_SECRET_KEY')
S3_ENDPOINT_URL = os.environ.get('S3_ENDPOINT_URL')

# Basic check to make sure env vars are set
if not all([S3_BUCKET_NAME, S3_ACCESS_KEY, S3_SECRET_KEY]):
    print("FATAL ERROR: S3_BUCKET_NAME, S3_ACCESS_KEY, and S3_SECRET_KEY")
    print("must be set as environment variables.")
    # In a real app, you might exit or raise an exception
    # For this example, we'll let it fail at request time.

# Initialize the S3 client
s3_client = boto3.client(
    's3',
    endpoint_url=S3_ENDPOINT_URL,
    aws_access_key_id=S3_ACCESS_KEY,
    aws_secret_access_key=S3_SECRET_KEY,
    config=Config(s3={'addressing_style': 'path'})
)

# Initialize the Flask web server
app = Flask(__name__)

# --- HTML/JS Template ---
# I've embedded your page here and modified the JavaScript.
# It now handles two types of data from the /data endpoint.
HTML_TEMPLATE = """
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Benchmark Results Viewer (Python Backend)</title>
    <!-- js-yaml is only needed if we parse YAML on client, which we don't -->
    <!-- <script src="https://cdnjs.cloudflare.com/ajax/libs/js-yaml/4.1.0/js-yaml.min.js"></script> -->
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
    <style>
        body { font-family: sans-serif; line-height: 1.6; margin: 20px; background-color: #f4f4f4; color: #333; }
        h1, h2, h3 { color: #0056b3; }
        .container { background-color: #fff; padding: 20px; border-radius: 8px; box-shadow: 0 0 10px rgba(0,0,0,0.1); }
        .file-input-section { display: none; } /* Hide the file upload */
        .benchmark-summary { margin-bottom: 30px; padding: 15px; border: 1px solid #ddd; border-radius: 5px; background-color: #f9f9f9; overflow-x: auto; }
        .benchmark-summary h3 { margin-top: 0; border-bottom: 1px solid #ccc; padding-bottom: 5px; }
        .metrics-grid { display: flex; flex-wrap: nowrap; gap: 15px; margin-top: 15px; overflow-x: auto; padding-bottom: 10px; }
        .metric-item { background-color: #eef; padding: 10px; border-radius: 4px; font-size: 0.9em; min-width: 150px; flex-shrink: 0; }
        .metric-item strong { display: block; color: #003366; margin-bottom: 5px; }
        .charts-container { display: grid; grid-template-columns: repeat(auto-fit, minmax(400px, 1fr)); gap: 20px; margin-top: 30px; }
        .chart-box { background-color: #fff; padding: 15px; border-radius: 5px; box-shadow: 0 0 5px rgba(0,0,0,0.05); }
        canvas { max-width: 100%; height: auto; }
        #results { margin-top: 20px; }
        #errorMessage { color: red; font-weight: bold; }
        
        /* New styles for LM-Eval table */
        .results-table {
            width: 100%;
            border-collapse: collapse;
            margin-top: 20px;
            box-shadow: 0 0 5px rgba(0,0,0,0.05);
        }
        .results-table th, .results-table td {
            border: 1px solid #ddd;
            padding: 10px;
            text-align: left;
        }
        .results-table th {
            background-color: #f2f2f2;
            color: #003366;
            font-weight: 600;
        }
        .results-table tbody tr:nth-child(even) {
            background-color: #f9f9f9;
        }
        .results-table td strong {
            color: #000;
        }

        /* NEW: Styles for rating badges */
        .rating-badge {
            padding: 3px 8px;
            border-radius: 12px;
            font-size: 0.9em;
            font-weight: 600;
            color: #fff;
            text-align: center;
        }
        .rating-good { background-color: #28a745; }
        .rating-moderate { background-color: #fd7e14; }
        .rating-poor { background-color: #dc3545; }
        .rating-na { background-color: #6c757d; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Benchmark Results Viewer</h1>
        <p id="errorMessage"></p>
        <div id="results">
            <p>Loading benchmark data from server...</p>
        </div>
    </div>

    <script>
        const resultsDiv = document.getElementById('results');
        const errorMessageDiv = document.getElementById('errorMessage');
        let charts = {}; // To keep track of Chart instances

        // --- MODIFIED SCRIPT ---
        // Runs on page load
        document.addEventListener('DOMContentLoaded', () => {
            const urlParams = new URLSearchParams(window.location.search);
            const fileKey = urlParams.get('file');

            if (!fileKey) {
                resultsDiv.innerHTML = '<p>Please specify a file in the URL (e.g., <code>?file=your-results.yaml</code> or <code>?file=your-results.json</code>)</p>';
                return;
            }

            // Fetch the data from our *own* server's /data endpoint
            fetch(`/data?file=${encodeURIComponent(fileKey)}`)
                .then(response => {
                    if (!response.ok) {
                        return response.json().then(err => { 
                            throw new Error(err.error || `Server error: ${response.status}`);
                        });
                    }
                    return response.json(); // Our server sends JSON
                })
                .then(data => {
                    // NEW: Check the fileType returned from the server
                    if (data && data.fileType === 'benchmark') {
                        errorMessageDiv.textContent = '';
                        // This is the original benchmark format
                        displayResults(data.data.benchmarks);
                    } else if (data && data.fileType === 'lm-eval') {
                        errorMessageDiv.textContent = '';
                        // This is the new lm-eval format
                        displayLmEvalResults(data.data);
                    } else {
                        throw new Error(data.error || "Invalid data structure from server: 'fileType' key not found or unrecognized.");
                    }
                })
                .catch(error => {
                    console.error("Error fetching or processing data:", error);
                    errorMessageDiv.textContent = `Error: ${error.message}`;
                    resultsDiv.innerHTML = '<p>Could not load or parse the data from the server.</p>';
                });
        });

        // ++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++
        // + NEW FUNCTION: Renders lm-evaluation-harness results
        // ++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++

        /**
         * NEW: Helper function to get a qualitative rating based on a metric
         */
        function getQualitativeRating(metricKey, value) {
            // Any metric that represents a standard error should show N/A
            if (
                metricKey.toLowerCase().includes('stderr') ||
                metricKey.toLowerCase().includes('stderror') ||
                metricKey.toLowerCase().endsWith('_stderr') ||
                typeof value !== 'number'
            ) {
                return '<span class="rating-badge rating-na">N/A</span>';
            }

            // Only apply qualitative rating to accuracy-type metrics
            if (metricKey.startsWith('acc')) {
                if (value > 0.6) {
                    return `<span class="rating-badge rating-good">Good</span>`;
                } else if (value > 0.3) {
                    return `<span class="rating-badge rating-moderate">Moderate</span>`;
                } else {
                    return `<span class="rating-badge rating-poor">Poor</span>`;
                }
            }

            // Fallback for metrics that we don't rate (e.g., loss, ppl)
            return '<span class="rating-badge rating-na">N/A</span>';
        }


        function displayLmEvalResults(data) {
            resultsDiv.innerHTML = ''; // Clear previous results

            const modelName = data.model_name_sanitized || data.config?.model_name || 'N/A';
            const totalTime = data.total_evaluation_time_seconds ? parseFloat(data.total_evaluation_time_seconds).toFixed(2) : 'N/A';
            
            // --- 1. Summary ---
            const summaryDiv = document.createElement('div');
            summaryDiv.className = 'benchmark-summary'; // Re-use existing style
            summaryDiv.innerHTML = `
                <h3>LM Evaluation Harness Results</h3>
                <p><strong>Model:</strong> ${modelName}</p>
                <p><strong>Total Evaluation Time:</strong> ${totalTime} seconds</p>
            `;
            resultsDiv.appendChild(summaryDiv);

            // --- 2. Results Table ---
            const resultsTable = document.createElement('table');
            resultsTable.className = 'results-table';
            resultsTable.innerHTML = `
                <thead>
                    <tr>
                        <th>Task (Alias)</th>
                        <th>Metric</th>
                        <th>Value</th>
                        <th>Rating</th> <!-- NEW COLUMN -->
                        <th>StdErr</th>
                        <th>n-shot</th>
                        <th>Version</th>
                    </tr>
                </thead>
                <tbody>
                </tbody>
            `;
            
            tbody = resultsTable.querySelector('tbody');

            if (!data.results || Object.keys(data.results).length === 0) {
                 tbody.innerHTML = '<tr><td colspan="7">No results found in file.</td></tr>'; // <-- Colspan 7
                 resultsDiv.appendChild(resultsTable);
                 return;
            }

            // Iterate over each task in the results
            for (const [taskName, taskData] of Object.entries(data.results)) {
                const alias = taskData.alias || taskName;
                const nShot = data['n-shot'] ? data['n-shot'][taskName] : 'N/A';
                const version = data.versions ? data.versions[taskName] : 'N/A';
                
                // Find all metric keys (those without _stderr and not 'alias')
                const metricKeys = Object.keys(taskData).filter(k => k !== 'alias' && !k.endsWith('_stderr'));

                if (metricKeys.length > 0) {
                    // For each metric (e.g., 'acc,none'), create a row
                    metricKeys.forEach((metricKey, index) => {
                        const value = taskData[metricKey];
                        
                        // NEW: Get qualitative rating
                        const rating = getQualitativeRating(metricKey, value);

                        // Try to find the corresponding stderr key
                        // e.g., 'acc,none' -> 'acc_stderr,none'
                        // e.g., 'f1' -> 'f1_stderr'
                        const stdErrKey = metricKey.includes(',') ? metricKey.replace(',', '_stderr,') : metricKey + '_stderr';
                        const stdErr = taskData[stdErrKey] ? taskData[stdErrKey].toFixed(4) : 'N/A';
                        
                        const row = document.createElement('tr');
                        
                        // Only print task name, n-shot, etc. on the first row for that task
                        row.innerHTML = `
                            <td>${index === 0 ? `<strong>${alias}</strong> (${taskName})` : ''}</td>
                            <td>${metricKey}</td>
                            <td>${typeof value === 'number' ? value.toFixed(4) : value}</td>
                            <td>${rating}</td> <!-- NEW CELL -->
                            <td>${stdErr}</td>
                            <td>${index === 0 ? nShot : ''}</td>
                            <td>${index === 0 ? version : ''}</td>
                        `;
                        tbody.appendChild(row);
                    });
                } else {
                    // Handle case where there are no obvious metrics, just show task
                    const row = document.createElement('tr');
                    row.innerHTML = `
                        <td><strong>${alias}</strong> (${taskName})</td>
                        <td colspan="4">No metrics found.</td> <!-- Colspan 4 -->
                        <td>${nShot}</td>
                        <td>${version}</td>
                    `;
                    tbody.appendChild(row);
                }
            }
            
            resultsDiv.appendChild(resultsTable);
        }


        // ++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++
        // + ORIGINAL FUNCTION: Renders benchmark results
        // ++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++++
        
        function displayResults(benchmarks) {
            resultsDiv.innerHTML = ''; // Clear previous results

            // --- 2. Per-Benchmark Summary ---
            benchmarks.forEach((benchmark, index) => {
                const summaryDiv = document.createElement('div');
                summaryDiv.className = 'benchmark-summary';

                const strategyType = benchmark.args?.strategy?.type_ || benchmark.args?.profile?.strategy_type || 'N/A';
                const strategyIndex = benchmark.args?.strategy_index ?? index;
                const duration = benchmark.duration ? benchmark.duration.toFixed(3) : 'N/A';
                const requests = benchmark.request_totals || { successful: 'N/A', incomplete: 'N/A', errored: 'N/A', total: 'N/A' };
                const totalRequests = (requests.successful || 0) + (requests.incomplete || 0) + (requests.errored || 0);

                summaryDiv.innerHTML = `
                    <h3>Benchmark ${strategyIndex}: ${strategyType.charAt(0).toUpperCase() + strategyType.slice(1)} Strategy</h3>
                    <p><strong>Duration:</strong> ${duration} seconds</p>
                    <p><strong>Requests:</strong>
                       Successful: ${requests.successful ?? 'N/A'},
                       Incomplete: ${requests.incomplete ?? 'N/A'},
                       Errored: ${requests.errored ?? 'N/A'},
                       Total: ${totalRequests}
                    </p>
                    <h4>Key Metrics (Total Requests):</h4>
                    <div class="metrics-grid">
                        ${generateMetricItem(benchmark.metrics, 'time_to_first_token_ms', 'TTFT (ms)')}
                        ${generateMetricItem(benchmark.metrics, 'time_per_output_token_ms', 'TPOT (ms/token)')}
                        ${generateMetricItem(benchmark.metrics, 'inter_token_latency_ms', 'Inter-Token Latency (ms)')}
                        ${generateMetricItem(benchmark.metrics, 'output_tokens_per_second', 'Output Tokens/sec')}
                        ${generateMetricItem(benchmark.metrics, 'requests_per_second', 'Requests/sec')}
                        ${generateMetricItem(benchmark.metrics, 'request_latency', 'Request Latency (s)')}
                        ${generateMetricItem(benchmark.metrics, 'request_concurrency', 'Avg Concurrency')}
                    </div>
                `;
                resultsDiv.appendChild(summaryDiv);
            });

             // --- 3. Charts ---
            const chartsContainer = document.createElement('div');
            chartsContainer.className = 'charts-container';
            resultsDiv.appendChild(chartsContainer);

            createComparisonChart(benchmarks, 'time_to_first_token_ms', 'Time to First Token (ms) - Lower is Better', 'TTFT', chartsContainer, true);
            createComparisonChart(benchmarks, 'time_per_output_token_ms', 'Time per Output Token (ms) - Lower is Better', 'TPOT', chartsContainer, true);
             createComparisonChart(benchmarks, 'inter_token_latency_ms', 'Inter-Token Latency (ms) - Lower is Better', 'ITL', chartsContainer, true);
            createComparisonChart(benchmarks, 'output_tokens_per_second', 'Output Tokens per Second - Higher is Better', 'OutputTokensPerSec', chartsContainer, false);
            createComparisonChart(benchmarks, 'requests_per_second', 'Requests per Second - Higher is Better', 'RPS', chartsContainer, false);
             createComparisonChart(benchmarks, 'request_latency', 'Request Latency (s) - Lower is Better', 'RequestLatency', chartsContainer, true);
             createComparisonChart(benchmarks, 'request_concurrency', 'Average Request Concurrency', 'AvgConcurrency', chartsContainer, false); 
        }

        function generateMetricItem(metrics, metricKey, displayName) {
            const metricData = metrics?.[metricKey]?.total;
            if (!metricData) {
                return `<div class="metric-item"><strong>${displayName}:</strong> N/A</div>`;
            }

            const isLatency = metricKey.includes('_ms') || metricKey === 'request_latency';
            const scaleFactor = (isLatency && metricKey !== 'request_latency') ? 1000 : 1; 
            const unitLabel = metricKey === 'request_latency' ? 's' : (metricKey.includes('_ms') ? 'ms' : ''); 

            return `
                <div class="metric-item">
                    <strong>${displayName}</strong>
                    Avg: ${metricData.mean != null ? (metricData.mean / scaleFactor).toFixed(2) : 'N/A'}${unitLabel}<br>
                    p50: ${metricData.median != null ? (metricData.median / scaleFactor).toFixed(2) : 'N/A'}${unitLabel}<br>
                    p99: ${metricData.percentiles?.p99 != null ? (metricData.percentiles.p99 / scaleFactor).toFixed(2) : 'N/A'}${unitLabel}
                </div>
            `;
        }

       function createComparisonChart(benchmarks, metricKey, title, chartIdBase, container, lowerIsBetter) {
            const labels = benchmarks.map((b, i) => {
                 const type = b.args?.strategy?.type_ || b.args?.profile?.strategy_type || 'N/A';
                 return `${b.args?.strategy_index ?? i}: ${type}`;
            });

            const dataPoints = { mean: [], p50: [], p99: [] };

            benchmarks.forEach(benchmark => {
                 const metricData = benchmark.metrics?.[metricKey]?.total;
                 const isLatency = metricKey.includes('_ms') || metricKey === 'request_latency';
                 const scaleFactor = (isLatency && metricKey !== 'request_latency') ? 1000 : 1; 

                 dataPoints.mean.push(metricData?.mean != null ? (metricData.mean / scaleFactor) : null);
                 dataPoints.p50.push(metricData?.median != null ? (metricData.median / scaleFactor) : null);
                 dataPoints.p99.push(metricData?.percentiles?.p99 != null ? (metricData.percentiles.p99 / scaleFactor) : null);
            });

            const validIndices = labels.map((_, i) => i).filter(i =>
                dataPoints.mean[i] != null || dataPoints.p50[i] != null || dataPoints.p99[i] != null
            );

            if (validIndices.length === 0) return;

            const filteredLabels = validIndices.map(i => labels[i]);
            const filteredMean = validIndices.map(i => dataPoints.mean[i]);
            const filteredP50 = validIndices.map(i => dataPoints.p50[i]);
            const filteredP99 = validIndices.map(i => dataPoints.p99[i]);

            const chartBox = document.createElement('div');
            chartBox.className = 'chart-box';
            const canvas = document.createElement('canvas');
            const chartId = `chart-${chartIdBase}`;
            canvas.id = chartId;
            chartBox.appendChild(canvas);
            container.appendChild(chartBox);

            const ctx = canvas.getContext('2d');

            if (charts[chartId]) {
                charts[chartId].destroy();
            }

             const datasets = [
                {
                    label: 'Mean', data: filteredMean,
                    backgroundColor: 'rgba(54, 162, 235, 0.6)', borderColor: 'rgba(54, 162, 235, 1)', borderWidth: 1
                },
                {
                    label: 'p50 (Median)', data: filteredP50,
                    backgroundColor: 'rgba(75, 192, 192, 0.6)', borderColor: 'rgba(75, 192, 192, 1)', borderWidth: 1
                },
                {
                    label: 'p99', data: filteredP99,
                    backgroundColor: 'rgba(255, 99, 132, 0.6)', borderColor: 'rgba(255, 99, 132, 1)', borderWidth: 1
                }
            ];

            charts[chartId] = new Chart(ctx, {
                type: 'bar',
                data: { labels: filteredLabels, datasets: datasets },
                options: {
                    responsive: true, maintainAspectRatio: true,
                    plugins: {
                        title: { display: true, text: title, font: { size: 16 } },
                         tooltip: {
                             callbacks: {
                                label: function(context) {
                                    let label = context.dataset.label || '';
                                    if (label) { label += ': '; }
                                    if (context.parsed.y !== null) {
                                        label += context.parsed.y.toFixed(3);
                                        if (metricKey === 'request_latency') label += ' s';
                                        else if (metricKey.includes('_ms')) label += ' ms';
                                    }
                                    return label;
                                }
                             }
                         }
                    },
                    scales: {
                        y: {
                            beginAtZero: true,
                            title: { display: true, text: metricKey.includes('_ms') ? 'Milliseconds (ms)' : (metricKey === 'request_latency' ? 'Seconds (s)' : 'Value') }
                        },
                        x: { title: { display: true, text: 'Benchmark Strategy' } }
                    },
                }
            });
        }
    </script>
</body>
</html>
"""

# --- API Endpoints ---

@app.route('/')
def index():
    """Serves the main HTML page."""
    # Renders the big string variable from above as HTML
    return render_template_string(HTML_TEMPLATE)


@app.route('/data')
def get_benchmark_data():
    """
    Securely fetches the file from S3, determines its type, 
    and returns it as JSON.
    """
    # 1. Get the requested filename from the URL query: ?file=...
    file_key = request.args.get('file')
    if not file_key:
        return jsonify({"error": "No 'file' parameter specified in URL."}), 400

    # 2. Check if server environment is configured
    if not S3_BUCKET_NAME or not s3_client:
        return jsonify({"error": "Server is not configured for S3 access."}), 500

    # 3. Try to fetch the file from S3
    try:
        s3_object = s3_client.get_object(Bucket=S3_BUCKET_NAME, Key=file_key)
        file_content = s3_object['Body'].read().decode('utf-8')

    except NoCredentialsError:
        return jsonify({"error": "Server S3 credentials are invalid or missing."}), 500
    except ClientError as e:
        if e.response['Error']['Code'] == 'NoSuchKey':
            return jsonify({"error": f"File not found in S3 bucket: {file_key}"}), 404
        else:
            # Other S3 error (e.g., Access Denied)
            return jsonify({"error": f"S3 Error: {e.response['Error']['Message']}"}), 500
    except Exception as e:
        return jsonify({"error": f"An unexpected error occurred: {str(e)}"}), 500

    # 4. NEW: Try to parse as JSON (for lm-eval) first
    try:
        data = json.loads(file_content)
        # Check for lm-eval structure
        if isinstance(data, dict) and 'results' in data and 'config' in data:
            return jsonify({
                "fileType": "lm-eval",
                "data": data,
                "fileName": file_key
            })
    except json.JSONDecodeError:
        # Not valid JSON, so we'll try YAML next
        pass
    except Exception as e:
        # Handle other unexpected errors during JSON check
        return jsonify({"error": f"Error during JSON processing: {str(e)}"}), 500

    # 5. NEW: If JSON fails, try to parse as YAML (for original benchmarks)
    try:
        data = yaml.safe_load(file_content)
        # Check for original benchmark structure
        if isinstance(data, dict) and 'benchmarks' in data:
            return jsonify({
                "fileType": "benchmark",
                "data": data,
                "fileName": file_key
            })
    except yaml.YAMLError as e:
        return jsonify({"error": f"Error parsing YAML file: {str(e)}"}), 500
    except Exception as e:
        # Handle other unexpected errors during YAML check
        return jsonify({"error": f"Error during YAML processing: {str(e)}"}), 500

    # 6. If neither format matches, return an error
    return jsonify({"error": "Unknown file structure. Expected 'benchmarks' key (for YAML) or 'results' and 'config' keys (for JSON)."}), 400

