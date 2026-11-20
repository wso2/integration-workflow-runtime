---
title: Deploy on AWS
description: Choose the database, compute, load balancer and identity provider for the Workflow Runtime on AWS.
keywords: [wso2 integrator, workflow runtime, aws, amazon rds, aurora, ecs, eks, ec2, cognito, nlb]
---

# Deploy on AWS

| Choice | Recommendation |
|---|---|
| Database | Amazon RDS for PostgreSQL (12 or later), or RDS for MySQL (8.0.17 or later), Aurora PostgreSQL or Aurora MySQL 3; with TLS ([Set up the database](database.md#amazon-rds-and-aurora)) |
| Compute | EC2 with the zip and the systemd unit ([Install the runtime](install.md)), or the image on ECS or EKS ([Run in containers](run-in-containers.md)) |
| Load balancer | A Network Load Balancer (TCP) on port 7233 in front of the frontend nodes: integrations keep long gRPC connections |
| Identity | Amazon Cognito or your own identity provider ([Secure the engine](secure-the-engine.md)) |
| Monitoring | Prometheus scraping each node, or the CloudWatch agent's Prometheus support ([Monitor the runtime](monitor.md)) |

## A single node to start

1. Create an RDS for PostgreSQL instance and a `workflow` user with the right to create databases.
2. Launch an EC2 instance in the same VPC, and allow it in the database's security group.
3. Install the runtime ([Install the runtime](install.md)), with `[database]` pointing at the instance and
   `[database.tls]` using the RDS certificate bundle.
4. Add `[engine.tls]` and `[engine.auth]`, and open port 7233 to the integrations' network only.

## Several nodes

Follow [Run across several nodes](scale-across-nodes.md): the same `runtime.toml` on every node, each with its own
`profiles` and address (`broadcastAddress = "auto"` on ECS and EKS), and `[engine.tls.internode]` on, which the
engine requires with `[engine.auth]`. Allow the nodes to reach each other on the
service ports (7233 to 7239) and membership ports (6933 to 6939), and place the Network Load Balancer in front of the
frontend nodes only.
