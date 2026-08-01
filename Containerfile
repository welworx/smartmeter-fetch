FROM gcr.io/distroless/static-debian12:nonroot

COPY smartmeter-fetch /smartmeter-fetch

EXPOSE 8790

ENTRYPOINT ["/smartmeter-fetch", "serve"]
